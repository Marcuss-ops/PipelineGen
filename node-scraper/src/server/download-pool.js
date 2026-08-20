const DEFAULT_VIDEO_CONCURRENCY = 4;
const DEFAULT_PROBE_CONCURRENCY = 2;

class BoundedPool {
  constructor(name, concurrency) {
    this.name = name;
    this.concurrency = Math.max(1, Number.parseInt(concurrency, 10) || 1);
    this.active = 0;
    this.pending = [];
    this.inFlight = new Map();
    this.completed = 0;
    this.failed = 0;
  }

  run(key, task) {
    if (key && this.inFlight.has(key)) return this.inFlight.get(key);
    const promise = new Promise((resolve, reject) => {
      this.pending.push({ key, task, resolve, reject });
      this.pump();
    });
    if (key) this.inFlight.set(key, promise);
    promise.finally(() => {
      if (key && this.inFlight.get(key) === promise) this.inFlight.delete(key);
    }).catch(() => {});
    return promise;
  }

  pump() {
    while (this.active < this.concurrency && this.pending.length > 0) {
      const job = this.pending.shift();
      this.active += 1;
      Promise.resolve()
        .then(job.task)
        .then((value) => {
          this.completed += 1;
          job.resolve(value);
        }, (error) => {
          this.failed += 1;
          job.reject(error);
        })
        .finally(() => {
          this.active -= 1;
          this.pump();
        });
    }
  }

  snapshot() {
    return {
      name: this.name,
      concurrency: this.concurrency,
      active: this.active,
      queued: this.pending.length,
      coalesced: this.inFlight.size,
      completed: this.completed,
      failed: this.failed,
    };
  }
}

export const globalVideoDownloadPool = new BoundedPool(
  'video-download',
  process.env.ARTLIST_GLOBAL_VIDEO_CONCURRENCY || DEFAULT_VIDEO_CONCURRENCY,
);

export const globalProbePool = new BoundedPool(
  'ffprobe',
  process.env.ARTLIST_FFPROBE_CONCURRENCY || DEFAULT_PROBE_CONCURRENCY,
);

export function downloadPoolSnapshot() {
  return {
    video: globalVideoDownloadPool.snapshot(),
    probe: globalProbePool.snapshot(),
  };
}
