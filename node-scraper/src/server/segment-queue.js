const MAX_SEGMENT_CONCURRENCY = 16;
const DEFAULT_SEGMENT_RATE = 20;

class SegmentQueue {
  constructor() {
    this.maxConcurrency = Math.max(1, Math.min(
      Number.parseInt(process.env.ARTLIST_GLOBAL_SEGMENT_CONCURRENCY || String(MAX_SEGMENT_CONCURRENCY), 10),
      MAX_SEGMENT_CONCURRENCY,
    ));
    this.ratePerSecond = Math.max(1, Number.parseInt(
      process.env.ARTLIST_GLOBAL_SEGMENT_RATE || String(DEFAULT_SEGMENT_RATE),
      10,
    ));
    this.intervalMs = 1000 / this.ratePerSecond;
    this.active = 0;
    this.nextTokenAt = 0;
    this.pending = [];
    this.timer = null;
  }

  run(task) {
    return new Promise((resolve, reject) => {
      this.pending.push({ task, resolve, reject });
      this.pump();
    });
  }

  pump() {
    if (this.active >= this.maxConcurrency || this.pending.length === 0) return;
    const waitMs = Math.max(0, this.nextTokenAt - Date.now());
    if (waitMs > 0) {
      if (!this.timer) {
        this.timer = setTimeout(() => {
          this.timer = null;
          this.pump();
        }, waitMs);
      }
      return;
    }

    const job = this.pending.shift();
    this.active += 1;
    this.nextTokenAt = Date.now() + this.intervalMs;
    Promise.resolve()
      .then(job.task)
      .then(job.resolve, job.reject)
      .finally(() => {
        this.active -= 1;
        this.pump();
      });
    this.pump();
  }
}

export const globalSegmentQueue = new SegmentQueue();
