import { platforms } from "./level";
export interface Movement {
  x: number;
  z: number;
  jump: boolean;
}
export class Player {
  x = 0;
  y = 0;
  z = 2;
  private vy = 0;
  private grounded = true;
  private jumpHeld = false;
  reset() {
    this.x = 0;
    this.y = 0;
    this.z = 2;
    this.vy = 0;
    this.grounded = true;
  }
  step(dt: number, input: Movement) {
    const length = Math.hypot(input.x, input.z) || 1;
    this.x += (input.x / length) * 7 * dt;
    this.z += (input.z / length) * 7 * dt;
    if (input.jump && !this.jumpHeld && this.grounded) {
      this.vy = 9;
      this.grounded = false;
    }
    this.jumpHeld = input.jump;
    const previousY = this.y;
    this.vy -= 22 * dt;
    this.y += this.vy * dt;
    const supported = platforms.some(
      (p) =>
        Math.abs(this.x - p.x) <= p.w / 2 && Math.abs(this.z - p.z) <= p.d / 2,
    );
    this.grounded = supported && previousY >= 0 && this.y <= 0 && this.vy <= 0;
    if (this.grounded) {
      this.y = 0;
      this.vy = 0;
    }
    if (this.y < -12) this.reset();
  }
}
