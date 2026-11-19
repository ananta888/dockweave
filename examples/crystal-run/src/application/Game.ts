import { Run } from "../domain/Run";
export interface BestTimeStore {
  load(): number | null;
  save(seconds: number): void;
}

export class Game {
  run: Run;
  best: number | null = null;
  storageAvailable = true;
  constructor(
    private readonly ids: readonly string[],
    private readonly store: BestTimeStore,
  ) {
    this.run = new Run(ids);
    try {
      this.best = store.load();
    } catch {
      this.storageAvailable = false;
    }
  }
  start() {
    this.run = new Run(this.ids);
    this.run.start();
  }
  complete() {
    if (!this.run.complete()) return false;
    if (this.best === null || this.run.seconds < this.best) {
      this.best = this.run.seconds;
      try {
        this.store.save(this.best);
      } catch {
        this.storageAvailable = false;
      }
    }
    return true;
  }
}
