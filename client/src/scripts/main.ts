import { drawBackdrop } from "./backdrop";
import { BoardView } from "./board";
import { botMove } from "./bot";
import { Connection } from "./connection";
import { LocalGame } from "./local";

// Dev builds reload the page whenever esbuild rebuilds (see build.mjs).
if (DEV) new EventSource("/esbuild").addEventListener("change", () => location.reload());
import { normalizeName, type Cell, type GameState, type Mark, type ServerMessage } from "./protocol";

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
  challenge: $("screen-challenge"),
  invite: $("screen-invite"),
  game: $("screen-game"),
};
type Screen = keyof typeof screens;

const ui = {
  playOnline: $<HTMLButtonElement>("play-online"),
  playFriend: $<HTMLButtonElement>("play-friend"),
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
  challengeLink: $("challenge-link"),
  copyLink: $<HTMLButtonElement>("copy-link"),
  shareLink: $<HTMLButtonElement>("share-link"),
  challengeCancel: $<HTMLButtonElement>("challenge-cancel"),
  inviteForm: $<HTMLFormElement>("invite-form"),
  inviteTitle: $("invite-title"),
  inviteName: $<HTMLInputElement>("invite-name"),
  inviteError: $("invite-error"),
  inviteAccept: $<HTMLButtonElement>("invite-accept"),
  inviteDecline: $<HTMLButtonElement>("invite-decline"),
  online: document.querySelectorAll<HTMLElement>(".online"),
  onlineCount: document.querySelectorAll<HTMLElement>(".online-count"),
  playerX: $("player-x"),
  playerO: $("player-o"),
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
type Action = "online" | "friend";

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
// The challenge code from a /c/<code> link this page was opened with, until
// the player accepts or declines it.
let inviteCode = location.pathname.match(/^\/c\/([a-z0-9]{4,12})\/?$/i)?.[1].toLowerCase() ?? null;
let botOfferTimer: number | undefined;

const BOT_OFFER_DELAY = 5_000;
const TITLE = document.title;

const key = ([x, y]: Cell) => `${x},${y}`;
const myTurn = () => !!game && !game.over && game.turn === game.you;
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
    for (const b of [ui.playOnline, ui.playFriend]) b.disabled = !connected;
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
      if (inviteCode) conn.send({ type: "peek", code: inviteCode });
      break;
    case "lobby":
      if (local) break; // e.g. a reconnect while playing on this device
      game = null;
      if (wantQueue && joinQueue()) break;
      wantQueue = false;
      // Opened from a challenge link: stay on the invite until it's answered.
      // A reconnect shouldn't throw the player off the name screen either.
      if (!inviteCode && screen !== "name") show("lobby");
      break;
    case "queued":
      game = null;
      wantQueue = true;
      show("queue");
      break;
    case "challenge":
      game = null;
      wantQueue = false;
      showChallenge(msg.code);
      break;
    case "invite":
      if (game && !game.over) break; // finish the running game first
      ui.inviteTitle.textContent = `${msg.name} challenges you`;
      ui.inviteAccept.dataset.ready = "true";
      ui.inviteAccept.disabled = !conn.connected;
      show("invite");
      break;
    case "game":
      wantQueue = false;
      if (inviteCode) clearInvite();
      updateGame(msg);
      break;
    case "error":
      if (msg.code === "name") {
        // The server rejected the username: ask for another one.
        wantQueue = false;
        store.set(NAME_KEY, "");
        if (inviteCode) {
          show("invite");
          ui.inviteError.textContent = msg.message;
        } else {
          askName(pendingAction, msg.message);
        }
      } else if (msg.code === "challenge") {
        if (inviteCode) {
          clearInvite();
          if (screen === "invite") show("lobby");
        }
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

for (const b of [ui.playOnline, ui.playFriend]) b.disabled = true;

ui.playOnline.addEventListener("click", () => start("online"));
ui.playFriend.addEventListener("click", () => start("friend"));
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
    case "friend":
      conn.send({ type: "challenge", name });
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

// ---- challenge a friend ----

function showChallenge(code: string): void {
  const url = `${location.origin}/c/${code}`;
  ui.challengeLink.textContent = url.replace(/^https?:\/\//, "");
  ui.challengeLink.dataset.url = url;
  ui.shareLink.hidden = !navigator.share;
  ui.copyLink.textContent = "Copy";
  show("challenge");
}

ui.copyLink.addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(ui.challengeLink.dataset.url ?? "");
    ui.copyLink.textContent = "Copied";
  } catch {
    // No clipboard access (e.g. plain HTTP): select the text to copy by hand.
    getSelection()?.selectAllChildren(ui.challengeLink);
  }
});

ui.shareLink.addEventListener("click", () => {
  const name = savedName();
  navigator
    .share({
      title: "amőba",
      text: `${name ? `${name} challenges you` : "You're challenged"} to a game of amőba – five in a row.`,
      url: ui.challengeLink.dataset.url,
    })
    .catch(() => {}); // the player closed the share sheet
});

ui.challengeCancel.addEventListener("click", () => {
  conn.send({ type: "cancel" });
  show("lobby");
});

// ---- invite (opened someone's challenge link) ----

if (inviteCode) {
  ui.inviteName.value = store.get(NAME_KEY) ?? "";
  show("invite");
} else {
  show("lobby");
}

function clearInvite(): void {
  inviteCode = null;
  ui.inviteAccept.dataset.ready = "false";
  history.replaceState(null, "", "/");
}

ui.inviteForm.addEventListener("submit", (e) => {
  e.preventDefault();
  const name = readName(ui.inviteName, ui.inviteError);
  if (!name || !inviteCode) return;
  store.set(NAME_KEY, name);
  conn.send({ type: "accept", code: inviteCode, name });
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
  if (isNew) board.recenter();
  board.setModel(g.moves, g.line ?? null, g.winner ?? null);

  if (preview && (!myTurn() || occupied.has(key(preview)))) setPreview(null);
  if (turnChanged && myTurn() && !isNew && g.mode !== "local") navigator.vibrate?.(30);

  renderHud();
  botTurn();
}

function setPreview(cell: Cell | null): void {
  preview = cell;
  board.setPreview(cell, game?.you ?? "X");
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
  board.setPreview(cell && myTurn() && !occupied.has(key(cell)) ? cell : null, game.you);
};

ui.recenter.addEventListener("click", () => board.recenter());

ui.leave.addEventListener("click", () => {
  if (local) {
    const inProgress = !!game && !game.over && game.moves.length > 0;
    if (inProgress && !confirm("Leave this game?")) return;
    endLocal();
    return;
  }
  if (game && !game.over && !confirm("Leave this game? It counts as a loss.")) return;
  wantQueue = false;
  conn.send({ type: "leave" });
});

ui.rematch.addEventListener("click", () => {
  // Against Amőbot, colours swap like an online rematch.
  if (local?.bot) startBot(local.bot === "X" ? "O" : "X");
  else if (local) startLocal();
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
  const opponent = g.opponentName || "Opponent";
  const bot = g.mode === "bot";

  for (const [el, mark] of [
    [ui.playerX, "X"],
    [ui.playerO, "O"],
  ] as const) {
    const mine = g.you === mark;
    el.querySelector(".pname")!.textContent = mine ? g.yourName : opponent;
    const tag = el.querySelector(".tag")!;
    const offline = !mine && !g.opponentConnected && !g.opponentLeft;
    tag.textContent = mine ? "you" : g.opponentLeft ? "left" : offline ? "offline" : bot ? "bot" : "";
    tag.classList.toggle("offline", offline || (!mine && g.opponentLeft));
    el.classList.toggle("active", !g.over && g.turn === mark);
  }

  // visibility (not display) so the HUD grid keeps its three columns. There's
  // no turn clock against Amőbot.
  ui.clock.style.visibility = g.over || bot ? "hidden" : "visible";
  ui.clock.dataset.turn = g.turn;
  ui.back.textContent = g.mode === "random" ? "Find a new opponent" : "Back to menu";

  if (g.over) {
    ui.status.textContent = "";
  } else if (myTurn()) {
    ui.status.textContent = preview
      ? "Tap the dashed mark again to place it"
      : coarsePointer
        ? "Your turn – tap a cell"
        : "Your turn – click a cell";
  } else {
    ui.status.textContent = bot
      ? `${opponent} is thinking…`
      : g.opponentConnected
        ? `Waiting for ${opponent}…`
        : `${opponent} is reconnecting…`;
  }

  ui.result.hidden = !g.over;
  if (g.over) {
    const won = g.winner === g.you;
    ui.resultTitle.textContent = won ? "You won! 🎉" : bot ? `${opponent} won` : "You lost";
    ui.resultTitle.className = g.winner === "X" ? "x" : "o";
    ui.resultDetail.textContent = resultDetail(g, won, opponent);

    // Once the opponent has gone there's no rematch: blur the button and,
    // if we'd already asked for one, say it's off.
    ui.rematch.disabled = g.opponentLeft || g.rematch.you;
    ui.rematch.classList.toggle("blurred", g.opponentLeft);
    ui.rematch.textContent = bot
      ? "Play again"
      : g.opponentLeft
        ? "Rematch"
        : g.rematch.you
          ? `Waiting for ${opponent}…`
          : g.rematch.opponent
            ? `${opponent} wants a rematch – accept`
            : "Rematch";
    ui.resultNote.hidden = !g.opponentLeft;
    ui.resultNote.textContent = g.rematch.you
      ? `Rematch cancelled – ${opponent} left.`
      : `${opponent} left – no rematch.`;
  }
}

const markName = (m: "X" | "O") => (m === "X" ? "Blue" : "Red");

function renderLocalHud(g: GameState): void {
  for (const [el, mark] of [
    [ui.playerX, "X"],
    [ui.playerO, "O"],
  ] as const) {
    el.querySelector(".pname")!.textContent = markName(mark);
    el.querySelector(".tag")!.textContent = "";
    el.classList.toggle("active", !g.over && g.turn === mark);
  }
  ui.clock.style.visibility = "hidden"; // no turn timer on a shared device

  ui.status.textContent = g.over
    ? ""
    : preview
      ? "Tap the dashed mark again to place it"
      : `${markName(g.turn)}'s turn – ${coarsePointer ? "tap" : "click"} a cell`;

  ui.result.hidden = !g.over;
  if (g.over && g.winner) {
    ui.resultTitle.textContent = `${markName(g.winner)} wins! 🎉`;
    ui.resultTitle.className = g.winner === "X" ? "x" : "o";
    ui.resultDetail.textContent = "Five in a row.";
    ui.resultNote.hidden = true;
    ui.rematch.disabled = false;
    ui.rematch.classList.remove("blurred");
    ui.rematch.textContent = "Play again";
    ui.back.textContent = "Back to menu";
  }
}

function resultDetail(g: GameState, won: boolean, opponent: string): string {
  switch (g.reason) {
    case "five":
      return won ? "Five in a row." : `${opponent} got five in a row.`;
    case "timeout":
      return won ? `${opponent} ran out of time.` : "You ran out of time.";
    case "resign":
      return won ? `${opponent} left the game.` : "You left the game.";
  }
  return "";
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
