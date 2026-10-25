import { drawBackdrop } from "./backdrop";
import { alternating, BoardView } from "./board";
import { botMove } from "./bot";
import { Connection } from "./connection";
import { LocalGame } from "./local";

// Dev builds reload the page whenever esbuild rebuilds (see build.mjs).
if (DEV) new EventSource("/esbuild").addEventListener("change", () => location.reload());
import {
  inReach,
  MAX_REACH,
  normalizeName,
  type Cell,
  type GameState,
  type Mark,
  type Player,
  type RoomState,
  type ServerMessage,
} from "./protocol";

// Offline support and "Add to Home Screen" (see sw.js). Dev builds skip it
// so cached files never get in the way of live reload.
if (!DEV && "serviceWorker" in navigator) {
  navigator.serviceWorker.register("/sw.js").catch(() => {});
}

const TOKEN_KEY = "amoeba.token";
const NAME_KEY = "amoeba.name";

// Storage can throw in private mode; the game still works without it.
const store = {
  get(key: string): string | null {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  },
  set(key: string, value: string): void {
    try {
      localStorage.setItem(key, value);
    } catch {}
  },
};

const $ = <T extends HTMLElement = HTMLElement>(id: string) => document.getElementById(id) as T;

const screens = {
  lobby: $("screen-lobby"),
  name: $("screen-name"),
  queue: $("screen-queue"),
  room: $("screen-room"),
  invite: $("screen-invite"),
  game: $("screen-game"),
};
type Screen = keyof typeof screens;

const ui = {
  playOnline: $<HTMLButtonElement>("play-online"),
  playFriends: $<HTMLButtonElement>("play-friends"),
  playBot: $<HTMLButtonElement>("play-bot"),
  playLocal: $<HTMLButtonElement>("play-local"),
  identity: document.querySelector<HTMLElement>(".identity")!,
  identityName: $("identity-name"),
  changeName: $<HTMLButtonElement>("change-name"),
  install: $<HTMLButtonElement>("install"),
  nameForm: $<HTMLFormElement>("name-form"),
  name: $<HTMLInputElement>("name"),
  nameError: $("name-error"),
  nameContinue: $<HTMLButtonElement>("name-continue"),
  nameBack: $<HTMLButtonElement>("name-back"),
  queueHint: $("queue-hint"),
  botOffer: $("bot-offer"),
  queueBot: $<HTMLButtonElement>("queue-bot"),
  cancel: $<HTMLButtonElement>("cancel"),
  roomTitle: $("room-title"),
  roomHint: $("room-hint"),
  roomLink: $("room-link"),
  roomCopy: $<HTMLButtonElement>("room-copy"),
  roomShare: $<HTMLButtonElement>("room-share"),
  roomPlayers: $("room-players"),
  roomStart: $<HTMLButtonElement>("room-start"),
  roomLeave: $<HTMLButtonElement>("room-leave"),
  inviteForm: $<HTMLFormElement>("invite-form"),
  inviteTitle: $("invite-title"),
  inviteHint: $("invite-hint"),
  inviteName: $<HTMLInputElement>("invite-name"),
  inviteError: $("invite-error"),
  inviteAccept: $<HTMLButtonElement>("invite-accept"),
  inviteDecline: $<HTMLButtonElement>("invite-decline"),
  online: document.querySelectorAll<HTMLElement>(".online"),
  onlineCount: document.querySelectorAll<HTMLElement>(".online-count"),
  hud: document.querySelector<HTMLElement>(".hud")!,
  // One per seat, in seat order: X, O, T, S.
  seats: [$("player-x"), $("player-o"), $("player-t"), $("player-s")],
  clock: $("clock"),
  clockText: $("clock-text"),
  clockBar: $("clock-bar"),
  status: $("status"),
  leave: $<HTMLButtonElement>("leave"),
  recenter: $<HTMLButtonElement>("recenter"),
  result: $("result"),
  resultTitle: $("result-title"),
  resultDetail: $("result-detail"),
  resultNote: $("result-note"),
  rematch: $<HTMLButtonElement>("rematch"),
  back: $<HTMLButtonElement>("back"),
  banner: $("banner"),
  bannerText: $("banner-text"),
  bannerAction: $<HTMLButtonElement>("banner-action"),
  toast: $("toast"),
};

const board = new BoardView($<HTMLCanvasElement>("board"));
drawBackdrop($<HTMLCanvasElement>("backdrop"));
const coarsePointer = matchMedia("(pointer: coarse)").matches;

/** The online ways to start a game; each needs a username first. */
type Action = "online" | "friends";

let screen: Screen = "lobby";
let game: GameState | null = null;
let occupied = new Set<string>();
let deadline = 0; // performance.now() timestamp when the current turn runs out
let preview: Cell | null = null;
// True while the player wants to be in the queue, so a reconnect that lands
// in the lobby (the server drops queued players on disconnect) rejoins.
let wantQueue = false;
// The same-device or Amőbot game in progress, if any. It reuses the game
// screen but never talks to the server.
let local: LocalGame | null = null;
let botTimer: number | undefined;
// What the player last asked for; the name screen carries on with it.
let pendingAction: Action | null = null;
// The code of the room link (/r/<code>) this page was opened with, until the
// player accepts or declines it.
let invite = location.pathname.match(/^\/r\/([a-z0-9]{4,12})\/?$/i)?.[1].toLowerCase() ?? null;
// The room the player is waiting in, if any.
let room: RoomState | null = null;
// The room a guest is waiting in, so a reconnect after the server has given
// up on them enters it again.
let wantRoom: string | null = null;
let botOfferTimer: number | undefined;

const BOT_OFFER_DELAY = 5_000;
const TITLE = document.title;

const key = ([x, y]: readonly number[]) => `${x},${y}`;
const me = (g: GameState) => g.players[g.you];
const myTurn = () => !!game && !game.over && game.turn === me(game).mark;
/** Whether the player may put a mark on cell: empty and near the others. */
const playable = (cell: Cell) =>
  !occupied.has(key(cell)) && (occupied.size === 0 || inReach(cell, (c) => occupied.has(key(c))));
const savedName = () => normalizeName(store.get(NAME_KEY) ?? "");

function show(next: Screen): void {
  for (const [name, el] of Object.entries(screens)) el.hidden = name !== next;
  document.title = next === "game" ? "amőba – playing" : TITLE;
  if (next === "queue" && screen !== "queue") {
    // Nobody might come; after a while offer a game against Amőbot.
    ui.botOffer.hidden = true;
    ui.queueHint.hidden = false;
    botOfferTimer = window.setTimeout(() => {
      ui.botOffer.hidden = false;
      ui.queueHint.hidden = true;
    }, BOT_OFFER_DELAY);
  } else if (next !== "queue") {
    clearTimeout(botOfferTimer);
  }
  if (next === "lobby") renderIdentity();
  screen = next;
}

// ---- connection ----

const wsUrl = `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws`;
let disconnectedTimer: number | undefined;

const conn = new Connection(wsUrl, {
  open() {
    conn.send({ type: "hello", token: store.get(TOKEN_KEY) ?? undefined });
  },
  status(connected) {
    for (const b of [ui.playOnline, ui.playFriends]) b.disabled = !connected;
    ui.inviteAccept.disabled = !connected || ui.inviteAccept.dataset.ready !== "true";
    clearTimeout(disconnectedTimer);
    if (connected) {
      hideBanner();
    } else if (!local) {
      // Don't flash the banner for blips that reconnect right away.
      disconnectedTimer = window.setTimeout(() => showBanner("Reconnecting…"), 1200);
    }
  },
  message: handleMessage,
});

function handleMessage(msg: ServerMessage): void {
  switch (msg.type) {
    case "welcome":
      store.set(TOKEN_KEY, msg.token);
      if (invite) conn.send({ type: "peekRoom", code: invite });
      break;
    case "lobby":
      if (local) break; // e.g. a reconnect while playing on this device
      game = null;
      room = null;
      if (wantQueue && joinQueue()) break;
      wantQueue = false;
      if (wantRoom && enterRoom(wantRoom)) break;
      wantRoom = null;
      // Opened from a link: stay on the invite until it's answered. A
      // reconnect shouldn't throw the player off the name screen either.
      if (!invite && screen !== "name") show("lobby");
      break;
    case "queued":
      game = null;
      wantQueue = true;
      show("queue");
      break;
    case "roomInvite": {
      if (game && !game.over) break; // finish the running game first
      const others = msg.players.filter((name) => name !== msg.host);
      ui.inviteTitle.textContent = `${msg.host} invites you to play`;
      ui.inviteHint.textContent = msg.playing
        ? "A game is in progress. Join now and you'll play in the next one."
        : `${others.length ? `${listNames(others)} ${others.length > 1 ? "are" : "is"} in. ` : ""}` +
          `Up to ${msg.max} players, five in a row on an endless board.`;
      ui.inviteAccept.dataset.ready = "true";
      ui.inviteAccept.disabled = !conn.connected;
      show("invite");
      break;
    }
    case "room":
      if (local) break; // playing on this device meanwhile
      game = null;
      wantQueue = false;
      if (invite) clearInvite();
      showRoom(msg);
      break;
    case "game":
      // An old online result that arrives on reconnect mustn't replace a
      // game on this device.
      if (local) break;
      wantQueue = false;
      wantRoom = null;
      room = null;
      if (invite) clearInvite();
      updateGame(msg);
      break;
    case "error":
      if (msg.code === "name") {
        // The server rejected the username: ask for another one.
        wantQueue = false;
        wantRoom = null;
        store.set(NAME_KEY, "");
        if (invite) {
          show("invite");
          ui.inviteError.textContent = msg.message;
        } else {
          askName(pendingAction, msg.message);
        }
      } else if (msg.code === "room") {
        // The link can't be used, or the room was closed.
        wantRoom = null;
        room = null;
        if (invite) {
          clearInvite();
          if (screen === "invite") show("lobby");
        }
        if (screen === "room") show("lobby");
        toast(msg.message);
      } else {
        toast(msg.message);
      }
      break;
    case "stats":
      for (const el of ui.onlineCount) el.textContent = String(msg.online);
      for (const el of ui.online) el.hidden = msg.online < Number(el.dataset.min ?? 1);
      break;
    case "kicked":
      conn.stop();
      clearTimeout(disconnectedTimer);
      showBanner("amőba is open in another tab.", "Use here", () => location.reload());
      break;
  }
}

// ---- home ----

for (const b of [ui.playOnline, ui.playFriends]) b.disabled = true;

ui.playOnline.addEventListener("click", () => start("online"));
ui.playFriends.addEventListener("click", () => start("friends"));
// These two work offline too: they need no server.
ui.playBot.addEventListener("click", () => startBot());
ui.playLocal.addEventListener("click", startLocal);
ui.changeName.addEventListener("click", () => askName(null));

function renderIdentity(): void {
  const name = savedName();
  ui.identity.hidden = !name;
  ui.identityName.textContent = name ?? "";
}

/** Starts an online game of the given kind, asking for a name first if needed. */
function start(action: Action): void {
  pendingAction = action;
  const name = savedName();
  if (!name) {
    askName(action);
    return;
  }
  switch (action) {
    case "online":
      wantQueue = true;
      joinQueue();
      break;
    case "friends":
      conn.send({ type: "room", name });
      break;
  }
}

function joinQueue(): boolean {
  const name = savedName();
  if (!name || !conn.send({ type: "join", name })) return false;
  show("queue");
  return true;
}

// ---- name ----

/** Shows the name screen; after a valid name it carries on with action. */
function askName(action: Action | null, error = ""): void {
  pendingAction = action;
  ui.name.value = store.get(NAME_KEY) || ui.name.value;
  ui.nameError.textContent = error;
  ui.nameContinue.textContent = action ? "Continue" : "Save";
  show("name");
  if (!coarsePointer) ui.name.focus();
}

/** Validates a name field, showing what's wrong; returns the clean name. */
function readName(input: HTMLInputElement, error: HTMLElement): string | null {
  const name = normalizeName(input.value);
  if (!name) {
    error.textContent = input.value.trim()
      ? "Use letters, digits, spaces and _ - . only (max 16)."
      : "Pick a username first.";
    input.focus();
    return null;
  }
  error.textContent = "";
  input.value = name;
  input.blur();
  return name;
}

ui.nameForm.addEventListener("submit", (e) => {
  e.preventDefault();
  const name = readName(ui.name, ui.nameError);
  if (!name) return;
  store.set(NAME_KEY, name);
  if (pendingAction) start(pendingAction);
  else show("lobby");
});

ui.name.addEventListener("input", () => (ui.nameError.textContent = ""));
ui.nameBack.addEventListener("click", () => {
  pendingAction = null;
  show("lobby");
});

// ---- queue ----

ui.cancel.addEventListener("click", () => {
  wantQueue = false;
  conn.send({ type: "cancel" });
  show("lobby");
});

ui.queueBot.addEventListener("click", () => {
  wantQueue = false;
  conn.send({ type: "cancel" });
  startBot();
});

// ---- room for playing with friends ----

ui.roomCopy.addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(ui.roomLink.dataset.url ?? "");
    ui.roomCopy.textContent = "Copied";
  } catch {
    // No clipboard access (e.g. plain HTTP): select the text to copy by hand.
    getSelection()?.selectAllChildren(ui.roomLink);
  }
});

ui.roomShare.addEventListener("click", () => {
  const host = room?.host;
  navigator
    .share({
      title: "amőba",
      text: `${host ? `${host} invites you` : "You're invited"} to play amőba – five in a row.`,
      url: ui.roomLink.dataset.url,
    })
    .catch(() => {}); // the player closed the share sheet
});

function showRoom(r: RoomState): void {
  room = r;
  const isHost = !!r.players[r.you]?.host;
  // A guest who drops out of the room enters it again on reconnect.
  wantRoom = isHost ? null : r.code;

  const url = `${location.origin}/r/${r.code}`;
  if (ui.roomLink.dataset.url !== url) {
    ui.roomLink.textContent = url.replace(/^https?:\/\//, "");
    ui.roomLink.dataset.url = url;
    ui.roomShare.hidden = !navigator.share;
    ui.roomCopy.textContent = "Copy";
  }

  const items = r.players.map((p, i) => {
    const li = document.createElement("li");
    li.textContent = p.name;
    const tag = document.createElement("span");
    tag.className = "tag";
    tag.textContent = [i === r.you && "you", p.host && "host", !p.connected && "offline"].filter(Boolean).join(" · ");
    tag.classList.toggle("offline", !p.connected);
    li.append(tag);
    return li;
  });
  if (!r.playing && r.players.length < r.min) {
    const li = document.createElement("li");
    li.className = "empty";
    li.textContent = "Waiting for a friend…";
    items.push(li);
  }
  ui.roomPlayers.replaceChildren(...items);

  const n = r.players.length;
  const host = r.players.find((p) => p.host);
  ui.roomTitle.textContent = r.playing ? "A game is in progress" : "Waiting for players…";
  ui.roomHint.textContent = r.playing
    ? `You'll play in the next one, when ${r.host} starts it.`
    : isHost
      ? `Send this link to up to ${r.max - 1} friends, and start when everyone's in.`
      : host?.connected
        ? `Waiting for ${r.host} to start the game.`
        : `${r.host} is reconnecting…`;
  ui.roomStart.hidden = !isHost;
  ui.roomStart.disabled = n < r.min;
  ui.roomStart.textContent = n < r.min ? `Start (needs ${r.min} players)` : `Start with ${n} players`;
  ui.roomLeave.textContent = isHost ? "Close the room" : "Leave";
  show("room");
}

/** Enters the room with the given code; false without a saved name. */
function enterRoom(code: string): boolean {
  const name = savedName();
  return !!name && conn.send({ type: "enter", code, name });
}

ui.roomStart.addEventListener("click", () => conn.send({ type: "start" }));
ui.roomLeave.addEventListener("click", () => {
  wantRoom = null;
  room = null;
  conn.send({ type: "cancel" });
  show("lobby");
});

// ---- invite (opened someone's room link) ----

if (invite) {
  ui.inviteName.value = store.get(NAME_KEY) ?? "";
  show("invite");
} else {
  show("lobby");
}

function clearInvite(): void {
  invite = null;
  ui.inviteAccept.dataset.ready = "false";
  history.replaceState(null, "", "/");
}

ui.inviteForm.addEventListener("submit", (e) => {
  e.preventDefault();
  const name = readName(ui.inviteName, ui.inviteError);
  if (!name || !invite) return;
  store.set(NAME_KEY, name);
  conn.send({ type: "enter", code: invite, name });
});

ui.inviteName.addEventListener("input", () => (ui.inviteError.textContent = ""));
ui.inviteDecline.addEventListener("click", () => {
  clearInvite();
  show("lobby");
});

// ---- same device and Amőbot ----

function startLocal(): void {
  local = new LocalGame();
  updateGame(local.state());
}

/** Starts a game against Amőbot, which plays `bot` (random by default). */
function startBot(bot: Mark = Math.random() < 0.5 ? "X" : "O"): void {
  local = new LocalGame(bot, savedName() ?? "You");
  updateGame(local.state());
}

// Amőbot replies after a short pause so the move doesn't appear instantly.
function botTurn(): void {
  const g = local;
  if (!g?.bot || g.over || g.toMove !== g.bot) return;
  const n = g.moves.length;
  clearTimeout(botTimer);
  botTimer = window.setTimeout(() => {
    if (local !== g || g.moves.length !== n) return; // left or restarted meanwhile
    g.play(botMove(g, g.bot!));
    updateGame(g.state());
  }, 400 + Math.random() * 400);
}

function endLocal(): void {
  clearTimeout(botTimer);
  local = null;
  game = null;
  setPreview(null);
  show("lobby");
  if (!conn.connected) disconnectedTimer = window.setTimeout(() => showBanner("Reconnecting…"), 1200);
}

// ---- game ----

function updateGame(g: GameState): void {
  const isNew = game?.id !== g.id;
  const turnChanged = isNew || game?.turn !== g.turn || game?.moves.length !== g.moves.length;
  game = g;
  occupied = new Set(g.moves.map(key));
  deadline = performance.now() + g.remainingMs;

  show("game");
  board.setModel(
    g.moves.map(([x, y]) => [x, y]),
    g.moves.map(([, , seat]) => g.players[seat].mark),
    g.line ?? null,
    g.winner ?? null,
  );
  if (isNew) board.recenter();

  if (preview && (!myTurn() || occupied.has(key(preview)))) setPreview(null);
  if (turnChanged && myTurn() && !isNew && g.mode !== "local") navigator.vibrate?.(30);

  renderHud();
  botTurn();
}

function setPreview(cell: Cell | null): void {
  preview = cell;
  board.setPreview(cell, game ? me(game).mark : "X");
}

function sendMove(cell: Cell): void {
  if (local) {
    setPreview(null);
    if (local.play(cell)) updateGame(local.state());
    return;
  }
  if (conn.send({ type: "move", x: cell[0], y: cell[1] })) setPreview(null);
}

board.onTap = (cell, pointerType) => {
  if (!game || !myTurn() || occupied.has(key(cell))) return;
  if (!playable(cell)) {
    toast(`Place your mark within ${MAX_REACH} cells of another mark.`);
    return;
  }
  // Mouse clicks are precise: place at once. Touch places on the second tap,
  // the first one only shows a dashed preview.
  if (pointerType === "mouse" || (preview && key(preview) === key(cell))) {
    sendMove(cell);
  } else {
    setPreview(cell);
  }
  renderHud();
};

board.onHover = (cell) => {
  if (!game) return;
  board.setPreview(cell && myTurn() && playable(cell) ? cell : null, me(game).mark);
};

ui.recenter.addEventListener("click", () => board.recenter());

ui.leave.addEventListener("click", () => {
  if (local) {
    const inProgress = !!game && !game.over && game.moves.length > 0;
    if (inProgress && !confirm("Leave this game?")) return;
    endLocal();
    return;
  }
  const playing = !!game && !game.over && !me(game).out;
  if (playing && !confirm("Leave this game? It counts as a loss.")) return;
  wantQueue = false;
  conn.send({ type: "leave" });
});

ui.rematch.addEventListener("click", () => {
  // Against Amőbot, colours swap like an online rematch.
  if (local?.bot) startBot(local.bot === "X" ? "O" : "X");
  else if (local) startLocal();
  else if (game?.mode === "room") conn.send({ type: "toRoom" });
  else conn.send({ type: "rematch" });
});

ui.back.addEventListener("click", () => {
  if (local) {
    endLocal();
  } else if (game?.mode === "random") {
    wantQueue = true;
    joinQueue();
  } else {
    wantQueue = false;
    conn.send({ type: "leave" });
  }
});

function renderHud(): void {
  if (!game) return;
  if (game.mode === "local") {
    renderLocalHud(game);
    return;
  }
  const g = game;
  const bot = g.mode === "bot";
  const duel = g.players.length === 2;
  const name = (p: Player) => p.name || "Opponent";
  const self = me(g);
  const opponents = g.players.filter((p) => p !== self);

  ui.hud.classList.toggle("multi", !duel);
  ui.seats.forEach((el, i) => {
    const p = g.players[i];
    el.hidden = !p;
    if (!p) return;
    const mine = p === self;
    el.querySelector(".pname")!.textContent = mine ? p.name : name(p);
    const tag = el.querySelector(".tag")!;
    const offline = !mine && !p.connected && !p.left;
    const state = p.left ? "left" : p.out ? "out" : offline ? "offline" : bot && !mine ? "bot" : "";
    tag.textContent = [mine && "you", state].filter(Boolean).join(" · ");
    tag.classList.toggle("offline", !mine && (offline || p.left || !!p.out));
    el.classList.toggle("out", !!p.out);
    el.classList.toggle("active", !g.over && g.turn === p.mark);
  });

  // visibility (not display) so the HUD grid keeps its three columns. There's
  // no turn clock against Amőbot.
  ui.clock.style.visibility = g.over || bot ? "hidden" : "visible";
  ui.clock.dataset.turn = g.turn;
  ui.back.textContent = g.mode === "random" ? "Find a new opponent" : "Back to menu";

  const current = g.players.find((p) => p.mark === g.turn)!;
  if (g.over) {
    ui.status.textContent = "";
  } else if (myTurn()) {
    ui.status.textContent = preview
      ? "Tap the dashed mark again to place it"
      : coarsePointer
        ? "Your turn – tap a cell"
        : "Your turn – click a cell";
  } else if (self.out) {
    ui.status.textContent = `You're out – ${name(current)} is playing`;
  } else {
    ui.status.textContent = bot
      ? `${name(current)} is thinking…`
      : current.connected
        ? `Waiting for ${name(current)}…`
        : `${name(current)} is reconnecting…`;
  }

  ui.result.hidden = !g.over;
  if (!g.over) return;

  const winner = g.players.find((p) => p.mark === g.winner);
  const won = winner === self;
  ui.resultTitle.textContent = !winner
    ? "It's a draw"
    : won
      ? "You won! 🎉"
      : bot || !duel
        ? `${name(winner)} won`
        : "You lost";
  ui.resultTitle.className = g.winner?.toLowerCase() ?? "";
  ui.resultDetail.textContent = resultDetail(g, winner, duel);

  if (g.mode === "room") {
    // No rematch with friends: everyone goes back to the room, where the
    // host starts the next game. It's gone once the host has left.
    ui.rematch.disabled = !g.room;
    ui.rematch.classList.toggle("blurred", !g.room);
    ui.rematch.textContent = "Back to the room";
    ui.resultNote.hidden = !!g.room;
    ui.resultNote.textContent = "The host has closed the room.";
    return;
  }

  // Once the opponent has gone there's no rematch: blur the button and, if
  // we'd already asked for one, say it's off.
  const opponent = opponents[0];
  ui.rematch.disabled = opponent.left || self.rematch;
  ui.rematch.classList.toggle("blurred", opponent.left);
  ui.rematch.textContent = bot
    ? "Play again"
    : opponent.left
      ? "Rematch"
      : self.rematch
        ? `Waiting for ${name(opponent)}…`
        : opponent.rematch
          ? `${name(opponent)} wants a rematch – accept`
          : "Rematch";
  ui.resultNote.hidden = !opponent.left;
  ui.resultNote.textContent = self.rematch
    ? `Rematch cancelled – ${name(opponent)} left.`
    : `${name(opponent)} left – no rematch.`;
}

/** "Anna", "Anna and Bence", "Anna, Bence and Csaba". */
function listNames(names: string[]): string {
  return names.length < 2 ? (names[0] ?? "") : `${names.slice(0, -1).join(", ")} and ${names.at(-1)}`;
}

const markName = (m: Mark) => ({ X: "Blue", O: "Red", T: "Green", S: "Amber" })[m];

function renderLocalHud(g: GameState): void {
  ui.hud.classList.remove("multi");
  ui.seats.forEach((el, i) => {
    const p = g.players[i];
    el.hidden = !p;
    if (!p) return;
    el.querySelector(".pname")!.textContent = markName(p.mark);
    el.querySelector(".tag")!.textContent = "";
    el.classList.remove("out");
    el.classList.toggle("active", !g.over && g.turn === p.mark);
  });
  ui.clock.style.visibility = "hidden"; // no turn timer on a shared device

  ui.status.textContent = g.over
    ? ""
    : preview
      ? "Tap the dashed mark again to place it"
      : `${markName(g.turn)}'s turn – ${coarsePointer ? "tap" : "click"} a cell`;

  ui.result.hidden = !g.over;
  if (g.over && g.winner) {
    ui.resultTitle.textContent = `${markName(g.winner)} wins! 🎉`;
    ui.resultTitle.className = g.winner.toLowerCase();
    ui.resultDetail.textContent = "Five in a row.";
    ui.resultNote.hidden = true;
    ui.rematch.disabled = false;
    ui.rematch.classList.remove("blurred");
    ui.rematch.textContent = "Play again";
    ui.back.textContent = "Back to menu";
  }
}

function resultDetail(g: GameState, winner: Player | undefined, duel: boolean): string {
  const won = winner === me(g);
  const name = winner?.name || "Opponent";
  if (g.reason === "draw") return "The board reached its move limit.";
  if (g.reason === "five") return won ? "Five in a row." : `${name} got five in a row.`;
  if (!duel) {
    // Everyone else ran out of time or left.
    return won ? "You're the last one left." : `${name} is the last one left.`;
  }
  const opponent = g.players.find((p) => p !== me(g))?.name || "Opponent";
  if (g.reason === "timeout") return won ? `${opponent} ran out of time.` : "You ran out of time.";
  return won ? `${opponent} left the game.` : "You left the game.";
}

// Turn clock: the server sends the remaining time with every state; we count
// down locally between updates.
function tick(): void {
  if (game && !game.over && !local) {
    const remaining = Math.max(0, deadline - performance.now());
    ui.clockText.textContent = String(Math.ceil(remaining / 1000));
    ui.clockBar.style.transform = `scaleX(${remaining / game.turnMs})`;
    ui.clock.classList.toggle("urgent", myTurn() && remaining < 10_000);
  }
  requestAnimationFrame(tick);
}
requestAnimationFrame(tick);

// ---- install ----

// Chrome and Edge offer installing through this event; Safari has no such
// prompt (Share → Add to Home Screen), so the button simply never shows there.
let installPrompt: (Event & { prompt(): Promise<unknown> }) | null = null;
addEventListener("beforeinstallprompt", (e) => {
  e.preventDefault();
  installPrompt = e as typeof installPrompt;
  ui.install.hidden = false;
});
addEventListener("appinstalled", () => {
  installPrompt = null;
  ui.install.hidden = true;
});
ui.install.addEventListener("click", () => {
  installPrompt?.prompt();
  installPrompt = null;
  ui.install.hidden = true;
});

// ---- banners ----

let toastTimer: number | undefined;
function toast(text: string): void {
  ui.toast.textContent = text;
  ui.toast.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => (ui.toast.hidden = true), 3500);
}

function showBanner(text: string, action?: string, onAction?: () => void): void {
  ui.bannerText.textContent = text;
  ui.bannerAction.hidden = !action;
  if (action) {
    ui.bannerAction.textContent = action;
    ui.bannerAction.onclick = onAction ?? null;
  }
  ui.banner.hidden = false;
}

function hideBanner(): void {
  ui.banner.hidden = true;
}

// iOS Safari ignores user-scalable=no; stop its page pinch-zoom explicitly.
document.addEventListener("gesturestart", (e) => e.preventDefault());
