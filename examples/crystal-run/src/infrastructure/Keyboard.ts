import type { Movement } from "../gameplay/Player";
export interface InputSource {
  read(): Movement;
  clear(): void;
}
export class Keyboard implements InputSource {
  private keys = new Set<string>();
  private down = (e: KeyboardEvent) => {
    if (
      ["Space", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight"].includes(
        e.code,
      )
    )
      e.preventDefault();
    this.keys.add(e.code);
  };
  private up = (e: KeyboardEvent) => {
    this.keys.delete(e.code);
  };
  constructor() {
    window.addEventListener("keydown", this.down);
    window.addEventListener("keyup", this.up);
  }
  read(): Movement {
    const has = (...keys: string[]) => keys.some((k) => this.keys.has(k));
    return {
      x: Number(has("KeyD", "ArrowRight")) - Number(has("KeyA", "ArrowLeft")),
      z: Number(has("KeyS", "ArrowDown")) - Number(has("KeyW", "ArrowUp")),
      jump: has("Space"),
    };
  }
  clear() {
    this.keys.clear();
  }
  dispose() {
    window.removeEventListener("keydown", this.down);
    window.removeEventListener("keyup", this.up);
  }
}
