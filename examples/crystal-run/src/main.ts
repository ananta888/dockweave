import "./style.css";
import { Game } from "./application/Game";
import { Player } from "./gameplay/Player";
import { crystals, portal, hazardAt } from "./gameplay/level";
import { Keyboard } from "./infrastructure/Keyboard";
import { LocalBestTimeStore } from "./infrastructure/LocalBestTimeStore";
import { World } from "./infrastructure/World";

const element = <T extends HTMLElement>(id: string) =>
  document.getElementById(id) as T;
const panel = element("panel"),
  message = element("message"),
  hud = element("hud");
const start = element<HTMLButtonElement>("start"),
  resume = element<HTMLButtonElement>("resume"),
  pause = element<HTMLButtonElement>("pause");
const game = new Game(
  crystals.map((c) => c.id),
  new LocalBestTimeStore(),
);
const player = new Player();
let world: World;
try {
  world = new World(element<HTMLCanvasElement>("world"));
} catch {
  message.textContent =
    "3D-Grafik ist nicht verfügbar. Bitte WebGL aktivieren oder einen aktuellen Browser verwenden.";
  start.disabled = true;
  throw new Error("WebGL initialization failed");
}
const input = new Keyboard();
const format = (seconds: number) => `${seconds.toFixed(1)} s`;
function updateUI() {
  const run = game.run;
  panel.hidden = run.status === "running";
  pause.hidden = run.status !== "running";
  resume.hidden = run.status !== "paused";
  start.textContent = run.status === "ready" ? "Starten" : "Neu starten";
  hud.textContent = `${run.count} / ${run.total} Kristalle · ${format(run.seconds)}${run.portalOpen ? " · Portal offen" : ""}`;
  if (run.status === "paused")
    message.textContent = "Pause. Bereit für den nächsten Sprung?";
  if (run.status === "completed")
    message.textContent = `Geschafft! Deine Zeit: ${format(run.seconds)}.`;
  element("best").textContent =
    `${game.best === null ? "Noch keine Bestzeit" : `Bestzeit: ${format(game.best)}`}${game.storageAvailable ? "" : " · Speicherung nicht verfügbar"}`;
}
function pauseGame() {
  game.run.pause();
  input.clear();
  updateUI();
}
start.onclick = () => {
  input.clear();
  player.reset();
  game.start();
  updateUI();
  start.blur();
};
resume.onclick = () => {
  game.run.resume();
  updateUI();
  resume.blur();
};
pause.onclick = pauseGame;
const keydown = (event: KeyboardEvent) => {
  if (event.code === "Escape" && !event.repeat) {
    if (game.run.status === "running") pauseGame();
    else if (game.run.status === "paused") {
      game.run.resume();
      updateUI();
    }
  }
};
const visibility = () => {
  if (document.hidden) pauseGame();
};
window.addEventListener("keydown", keydown);
window.addEventListener("blur", pauseGame);
document.addEventListener("visibilitychange", visibility);
let previous = performance.now(),
  accumulator = 0,
  frame = 0;
function animate(now: number) {
  const dt = Math.min((now - previous) / 1000, 0.1);
  previous = now;
  if (game.run.status === "running") {
    accumulator += dt;
    while (accumulator >= 1 / 60 && game.run.status === "running") {
      game.run.tick(1 / 60);
      player.step(1 / 60, input.read());
      const hazard = hazardAt(game.run.seconds);
      if (
        player.y < 1.5 &&
        Math.hypot(player.x - hazard.x, player.z - hazard.z) < 1
      )
        player.reset();
      for (const c of crystals)
        if (
          Math.hypot(player.x - c.x, player.z - c.z) < 1 &&
          Math.abs(player.y) < 1.8
        )
          game.run.collect(c.id);
      if (
        Math.hypot(player.x - portal.x, player.z - portal.z) < 1.2 &&
        Math.abs(player.y) < 1.8
      )
        game.complete();
      accumulator -= 1 / 60;
    }
  } else accumulator = 0;
  updateUI();
  world.render(player, game.run, dt);
  frame = requestAnimationFrame(animate);
}
updateUI();
frame = requestAnimationFrame(animate);
window.addEventListener("pageshow", (event) => {
  if (event.persisted) window.location.reload();
});
window.addEventListener(
  "pagehide",
  () => {
    cancelAnimationFrame(frame);
    input.dispose();
    world.dispose();
    window.removeEventListener("keydown", keydown);
    window.removeEventListener("blur", pauseGame);
    document.removeEventListener("visibilitychange", visibility);
  },
  { once: true },
);
