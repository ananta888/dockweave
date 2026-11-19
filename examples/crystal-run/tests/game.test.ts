import { describe, expect, it } from "vitest";
import { Run } from "../src/domain/Run";
import { Game, type BestTimeStore } from "../src/application/Game";
import { Player } from "../src/gameplay/Player";
import { platforms } from "../src/gameplay/level";

describe("run rules", () => {
  it("accepts only known, unique crystals during an active run", () => {
    const run = new Run(["a", "b"]);
    expect(run.collect("a")).toBe(false);
    run.start();
    expect(run.complete()).toBe(false);
    expect(run.collect("unknown")).toBe(false);
    expect(run.collect("a")).toBe(true);
    expect(run.collect("a")).toBe(false);
    run.pause();
    expect(run.collect("b")).toBe(false);
    run.tick(10);
    expect(run.seconds).toBe(0);
    run.resume();
    run.tick(2);
    run.collect("b");
    expect(run.portalOpen).toBe(true);
    expect(run.complete()).toBe(true);
    expect(run.complete()).toBe(false);
    run.tick(5);
    expect(run.seconds).toBe(2);
  });
  it("rejects invalid level IDs and time steps", () => {
    expect(() => new Run([])).toThrow();
    expect(() => new Run(["a", "a"])).toThrow();
    const run = new Run(["a"]);
    for (const dt of [-1, NaN, Infinity]) expect(() => run.tick(dt)).toThrow();
  });
});
describe("best time", () => {
  it("saves only improvements and starts fresh rounds", () => {
    let stored = 10;
    const store: BestTimeStore = {
      load: () => stored,
      save: (value) => {
        stored = value;
      },
    };
    const game = new Game(["a"], store);
    for (const duration of [12, 8, 9]) {
      game.start();
      game.run.tick(duration);
      game.run.collect("a");
      game.complete();
    }
    expect(stored).toBe(8);
    game.start();
    expect(game.run.count).toBe(0);
    expect(game.run.seconds).toBe(0);
  });
  it("allows completion when browser storage fails", () => {
    const game = new Game(["a"], {
      load() {
        throw Error("denied");
      },
      save() {
        throw Error("full");
      },
    });
    game.start();
    game.run.collect("a");
    expect(game.complete()).toBe(true);
    expect(game.storageAvailable).toBe(false);
    expect(game.run.status).toBe("completed");
  });
});
describe("movement", () => {
  it("can jump across every gap along the island route", () => {
    for (let i = 0; i < platforms.length - 1; i++) {
      const from = platforms[i],
        to = platforms[i + 1];
      const player = new Player();
      player.x = from.x;
      player.z = from.z - from.d / 2 + 0.15;
      const targetX = Math.max(
        to.x - to.w / 2 + 0.5,
        Math.min(from.x, to.x + to.w / 2 - 0.5),
      );
      for (let step = 0; step < 54; step++)
        player.step(1 / 60, {
          x:
            Math.abs(targetX - player.x) > 0.1
              ? Math.sign(targetX - player.x)
              : 0,
          z: -1,
          jump: step === 0,
        });
      expect(player.y).toBe(0);
      expect(player.z).toBeLessThan(to.z + to.d / 2);
    }
  });
  it("returns to spawn after falling", () => {
    const player = new Player();
    player.x = 100;
    for (let i = 0; i < 120; i++)
      player.step(1 / 60, { x: 0, z: 0, jump: false });
    expect(player.x).toBe(0);
    expect(player.y).toBe(0);
  });
});
