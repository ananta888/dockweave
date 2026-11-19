import type { BestTimeStore } from "../application/Game";
export class LocalBestTimeStore implements BestTimeStore {
  load() {
    const raw = localStorage.getItem("crystal-run.best.v1");
    if (raw === null) return null;
    const value = Number(raw);
    return raw.trim() && Number.isFinite(value) && value >= 0 ? value : null;
  }
  save(seconds: number) {
    localStorage.setItem("crystal-run.best.v1", String(seconds));
  }
}
