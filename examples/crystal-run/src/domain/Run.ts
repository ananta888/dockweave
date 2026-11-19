export type RunStatus = "ready" | "running" | "paused" | "completed";

/** Aggregate: the only authority for collection, timing and completion. */
export class Run {
  private collected = new Set<string>();
  private state: RunStatus = "ready";
  private elapsed = 0;
  private readonly required: ReadonlySet<string>;

  constructor(crystalIds: readonly string[]) {
    if (!crystalIds.length || new Set(crystalIds).size !== crystalIds.length) {
      throw new Error("A level needs unique crystal IDs");
    }
    this.required = new Set(crystalIds);
  }
  get status() {
    return this.state;
  }
  get seconds() {
    return this.elapsed;
  }
  get count() {
    return this.collected.size;
  }
  get total() {
    return this.required.size;
  }
  get portalOpen() {
    return this.count === this.total;
  }
  has(id: string) {
    return this.collected.has(id);
  }
  start() {
    if (this.state === "ready") this.state = "running";
  }
  pause() {
    if (this.state === "running") this.state = "paused";
  }
  resume() {
    if (this.state === "paused") this.state = "running";
  }
  tick(seconds: number) {
    if (!Number.isFinite(seconds) || seconds < 0)
      throw new Error("Invalid time step");
    if (this.state === "running") this.elapsed += seconds;
  }
  collect(id: string): boolean {
    if (this.state !== "running" || !this.required.has(id) || this.has(id))
      return false;
    this.collected.add(id);
    return true;
  }
  complete(): boolean {
    if (this.state !== "running" || !this.portalOpen) return false;
    this.state = "completed";
    return true;
  }
}
