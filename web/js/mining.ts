// Consent-gated, opt-in Monero (RandomX) browser miner for Anubis.
//
// Nothing here runs until the visitor explicitly clicks "Accept". The miner
// opens a WebSocket to Anubis (which bridges to the configured pool), spawns a
// pool of RandomX workers, distributes jobs to them, and submits the shares
// they find. Once the pool has accepted enough shares, the visitor is
// redirected through the normal Anubis pass-challenge flow.

interface MiningConfig {
  requiredShares: number;
  threads: number;
  throttle: number;
  wasmURL: string;
  wsPath: string;
  pool: string;
}

const j = (id: string): any | null => {
  const elem = document.getElementById(id);
  if (elem === null) {
    return null;
  }
  return JSON.parse(elem.textContent);
};

const cfg: MiningConfig | null = j("anubis_mining");
const passURL: string | null = j("anubis_mining_passurl");
const basePrefix: string = j("anubis_base_prefix") ?? "";
const anubisVersion: string = j("anubis_version") ?? "";

const acceptBtn = document.getElementById("mining-accept") as HTMLButtonElement;
const refuseBtn = document.getElementById("mining-refuse") as HTMLButtonElement;
const consent = document.getElementById("mining-consent") as HTMLDivElement;
const progressWrap = document.getElementById(
  "mining-progress",
) as HTMLDivElement;
const status = document.getElementById("status") as HTMLParagraphElement;
const progress = document.getElementById("progress") as HTMLDivElement;

const setProgress = (shares: number, required: number) => {
  const pct = Math.min(100, (shares / required) * 100);
  const inner = progress.firstElementChild as HTMLElement | null;
  if (inner) {
    inner.style.width = `${pct}%`;
  }
  progress.setAttribute("aria-valuenow", String(pct));
};

const hardwareThreads = () =>
  navigator.hardwareConcurrency !== undefined
    ? Math.max(1, Math.trunc(navigator.hardwareConcurrency / 2))
    : 1;

const start = () => {
  if (!cfg || !passURL) {
    return;
  }

  consent.style.display = "none";
  progressWrap.style.display = "block";
  progress.style.display = "block";
  status.textContent = `Connecting… 0/${cfg.requiredShares} shares`;

  const threads = cfg.threads > 0 ? cfg.threads : hardwareThreads();
  const workerURL = `${basePrefix}/.within.website/x/cmd/anubis/static/js/worker/randomx.mjs?cacheBuster=${anubisVersion}`;

  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  const challengeId = j("anubis_challenge")?.challenge?.id;
  const wsURL = `${proto}//${window.location.host}${cfg.wsPath}?id=${encodeURIComponent(challengeId)}`;

  const ws = new WebSocket(wsURL);
  const workers: Worker[] = [];
  let currentJob: any = null;
  let finished = false;

  const cleanup = () => {
    finished = true;
    workers.forEach((w) => w.terminate());
    try {
      ws.close();
    } catch (_e) {
      // ignore
    }
  };

  const fail = (msg: string) => {
    if (finished) {
      return;
    }
    cleanup();
    status.textContent = msg;
  };

  ws.onopen = () => {
    status.textContent = `Mining… 0/${cfg.requiredShares} shares`;
    for (let i = 0; i < threads; i++) {
      const worker = new Worker(workerURL, { type: "module" });
      worker.onmessage = (event) => {
        const data = event.data;
        if (data.type === "share") {
          ws.send(
            JSON.stringify({
              type: "submit",
              job_id: data.job_id,
              nonce: data.nonce,
              result: data.result,
            }),
          );
        }
      };
      worker.onerror = () => fail("Mining worker failed to load.");
      worker.postMessage({
        type: "config",
        wasmURL: cfg.wasmURL,
        throttle: cfg.throttle,
        nonceOffset: i,
        nonceStride: threads,
      });
      workers.push(worker);
    }
  };

  ws.onmessage = (event) => {
    let msg: any;
    try {
      msg = JSON.parse(event.data);
    } catch (_e) {
      return;
    }

    switch (msg.type) {
      case "job":
        currentJob = msg.job;
        workers.forEach((w) =>
          w.postMessage({ type: "job", job: currentJob }),
        );
        break;
      case "accepted":
        setProgress(msg.shares, msg.required);
        status.textContent = `Mining… ${msg.shares}/${msg.required} shares`;
        break;
      case "rejected":
        // Pool rejected a stale/low share; keep going silently.
        break;
      case "done":
        setProgress(msg.required, msg.required);
        status.textContent = "Done! Granting access…";
        cleanup();
        window.location.replace(passURL);
        break;
      case "error":
        fail(`Mining error: ${msg.message ?? "unknown"}`);
        break;
    }
  };

  ws.onerror = () => fail("Connection to mining bridge failed.");
  ws.onclose = () => {
    if (!finished) {
      fail("Connection to mining bridge closed.");
    }
  };
};

if (acceptBtn) {
  acceptBtn.addEventListener("click", start, { once: true });
}
if (refuseBtn) {
  refuseBtn.addEventListener("click", () => {
    consent.innerHTML =
      "<p>Access refused. This site requires in-browser mining to enter.</p>";
  });
}
