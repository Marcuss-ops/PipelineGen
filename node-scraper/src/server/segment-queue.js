const MAX_SEGMENT_CONCURRENCY = 16;
const DEFAULT_SEGMENT_RATE = 20;

class SegmentQueue {
  constructor() {
    this.maxConcurrency = Math.max(1, Math.min(
      Number.parseInt(process.env.ARTLIST_GLOBAL_SEGMENT_CONCURRENCY || '6', 10),
      MAX_SEGMENT_CONCURRENCY,
    ));
    this.ratePerSecond = Math.max(1, Number.parseInt(
      process.env.ARTLIST_GLOBAL_SEGMENT_RATE || String(DEFAULT_SEGMENT_RATE),
      10,
    ));
    this.intervalMs = 1000 / this.ratePerSecond;
    this.adaptive = process.env.ARTLIST_ADAPTIVE_SEGMENTS !== 'false';
    this.active = 0;
    this.nextTokenAt = 0;
    this.pending = [];
    this.timer = null;
    this.successesSinceAdjustment = 0;
    this.completed = 0;
    this.failed = 0;
    this.retries = 0;
    this.status403 = 0;
    this.status429 = 0;
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
      .then((value) => {
        this.completed += 1;
        this.successesSinceAdjustment += 1;
        if (this.adaptive && this.successesSinceAdjustment >= 20 && this.maxConcurrency < 12) {
          this.maxConcurrency += 1;
          this.successesSinceAdjustment = 0;
        }
        job.resolve(value);
      }, (error) => {
        this.failed += 1;
        if (error?.status === 403) this.status403 += 1;
        if (error?.status === 429) this.status429 += 1;
        if (this.adaptive && (error?.status === 403 || error?.status === 429)) {
          this.maxConcurrency = Math.max(2, this.maxConcurrency - 2);
          this.successesSinceAdjustment = 0;
        }
        job.reject(error);
      })
      .finally(() => {
        this.active -= 1;
        this.pump();
      });
    this.pump();
  }

  snapshot() {
    return {
      active: this.active,
      queued: this.pending.length,
      max_concurrency: this.maxConcurrency,
      rate_per_second: this.ratePerSecond,
      adaptive: this.adaptive,
      completed: this.completed,
      failed: this.failed,
      status_403: this.status403,
      status_429: this.status429,
    };
  }
}

export const globalSegmentQueue = new SegmentQueue();

export function segmentQueueSnapshot() {
  return globalSegmentQueue.snapshot();
}
