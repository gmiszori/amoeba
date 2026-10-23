import { BoardView } from "./board";
import { Connection } from "./connection";
import { LocalGame } from "./local";

// Dev builds reload the page whenever esbuild rebuilds (see build.mjs).
if (DEV) new EventSource("/esbuild").addEventListener("change", () => location.reload());
import { normalizeName, type Cell, type GameState, type ServerMessage } from "./protocol";

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
  queue: $("screen-queue"),
  game: $("screen-game"),
};
type Screen = keyof typeof screens;

const ui = {
  form: $<HTMLFormElement>("join-form"),
  name: $<HTMLInputElement>("name"),
  nameError: $("name-error"),
  join: $<HTMLButtonElement>("join"),
  cancel: $<HTMLButtonElement>("cancel"),
  local: $<HTMLButtonElement>("local"),
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
const coarsePointer = matchMedia("(pointer: coarse)").matches;

let game: GameState | null = null;
let occupied = new Set<string>();
let deadline = 0; // performance.now() timestamp when the current turn runs out
let preview: Cell | null = null;
// True while the player wants to be in the queue, so a reconnect that lands
// in the lobby (the server drops queued players on disconnect) rejoins.
let wantQueue = false;
// The same-device game in progress, if any. It reuses the game screen but
// never talks to the server.
let local: LocalGame | null = null;

const key = ([x, y]: Cell) => `${x},${y}`;
const myTurn = () => !!game && !game.over && game.turn === game.you;

function show(screen: Screen): void {
  for (const [name, el] of Object.entries(screens)) el.hidden = name !== screen;
  document.title = screen === "game" ? "amőba – playing" : "amőba";
}

// ---- connection ----

const wsUrl = `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws`;
let disconnectedTimer: number | undefined;

const conn = new Connection(wsUrl, {
  open() {
    conn.send({ type: "hello", token: store.get(TOKEN_KEY) ?? undefined });
  },
  status(connected) {
    ui.join.disabled = !connected;
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
      break;
    case "lobby":
      if (local) break; // e.g. a reconnect while playing on this device
      game = null;
      if (wantQueue && joinQueue()) break;
      wantQueue = false;
      show("lobby");
      break;
    case "queued": {
      // Only remember names the server accepted.
      const name = normalizeName(ui.name.value);
      if (name) store.set(NAME_KEY, name);
      game = null;
      wantQueue = true;
      show("queue");
      break;
    }
    case "game":
      wantQueue = false;
      updateGame(msg);
      break;
    case "error":
      if (msg.code === "name") {
        // The server rejected the username: back to the lobby to pick another.
        wantQueue = false;
        show("lobby");
        ui.nameError.textContent = msg.message;
      } else {
        toast(msg.message);
      }
      break;
    case "stats":
      for (const el of ui.onlineCount) el.textContent = String(msg.online);
      for (const el of ui.online) el.hidden = false;
      break;
    case "kicked":
      conn.stop();
      clearTimeout(disconnectedTimer);
      showBanner("amőba is open in another tab.", "Use here", () => location.reload());
      break;
  }
}

// ---- lobby & queue ----

ui.name.value = store.get(NAME_KEY) ?? "";
ui.join.disabled = true;

ui.form.addEventListener("submit", (e) => {
  e.preventDefault();
  const name = normalizeName(ui.name.value);
  if (!name) {
    ui.nameError.textContent = ui.name.value.trim()
      ? "Use letters, digits, spaces and _ - . only (max 16)."
      : "Pick a username first.";
    ui.name.focus();
    return;
  }
  ui.nameError.textContent = "";
  ui.name.value = name;
  ui.name.blur();
  wantQueue = true;
  joinQueue();
});

ui.name.addEventListener("input", () => (ui.nameError.textContent = ""));

ui.cancel.addEventListener("click", () => {
  wantQueue = false;
  conn.send({ type: "cancel" });
  show("lobby");
});

// Works offline too: a local game needs no server.
ui.local.addEventListener("click", startLocal);

function startLocal(): void {
  local = new LocalGame();
  updateGame(local.state());
}

function endLocal(): void {
  local = null;
  game = null;
  setPreview(null);
  show("lobby");
  if (!conn.connected) disconnectedTimer = window.setTimeout(() => showBanner("Reconnecting…"), 1200);
}

function joinQueue(): boolean {
  const name = normalizeName(ui.name.value);
  if (!name || !conn.send({ type: "join", name })) return false;
  show("queue");
  return true;
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
  if (turnChanged && myTurn() && !isNew && !local) navigator.vibrate?.(30);

  renderHud();
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
  if (local) startLocal();
  else conn.send({ type: "rematch" });
});

ui.back.addEventListener("click", () => {
  if (local) {
    endLocal();
    return;
  }
  wantQueue = true;
  joinQueue();
});

function renderHud(): void {
  if (!game) return;
  if (local) {
    renderLocalHud(game);
    return;
  }
  const g = game;
  const opponent = g.opponentName || "Opponent";

  for (const [el, mark] of [
    [ui.playerX, "X"],
    [ui.playerO, "O"],
  ] as const) {
    const mine = g.you === mark;
    el.querySelector(".pname")!.textContent = mine ? g.yourName : opponent;
    const tag = el.querySelector(".tag")!;
    const offline = !mine && !g.opponentConnected && !g.opponentLeft;
    tag.textContent = mine ? "you" : g.opponentLeft ? "left" : offline ? "offline" : "";
    tag.classList.toggle("offline", offline || (!mine && g.opponentLeft));
    el.classList.toggle("active", !g.over && g.turn === mark);
  }

  // visibility (not display) so the HUD grid keeps its three columns.
  ui.clock.style.visibility = g.over ? "hidden" : "visible";
  ui.clock.dataset.turn = g.turn;
  ui.back.textContent = "Back to matchmaking";

  if (g.over) {
    ui.status.textContent = "";
  } else if (myTurn()) {
    ui.status.textContent = preview
      ? "Tap the dashed mark again to place it"
      : coarsePointer
        ? "Your turn – tap a cell"
        : "Your turn – click a cell";
  } else {
    ui.status.textContent = g.opponentConnected
      ? `Waiting for ${opponent}…`
      : `${opponent} is reconnecting…`;
  }

  ui.result.hidden = !g.over;
  if (g.over) {
    const won = g.winner === g.you;
    ui.resultTitle.textContent = won ? "You won!" : "You lost";
    ui.resultTitle.className = g.winner === "X" ? "x" : "o";
    ui.resultDetail.textContent = resultDetail(g, won, opponent);

    // Once the opponent has gone there's no rematch: blur the button and,
    // if we'd already asked for one, say it's off.
    ui.rematch.disabled = g.opponentLeft || g.rematch.you;
    ui.rematch.classList.toggle("blurred", g.opponentLeft);
    ui.rematch.textContent = g.opponentLeft
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
    ui.resultTitle.textContent = `${markName(g.winner)} wins!`;
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

// ---- banners ----

let toastTimer: number | undefined;
function toast(text: string): void {
  ui.toast.textContent = text;
  ui.toast.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => (ui.toast.hidden = true), 3000);
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
