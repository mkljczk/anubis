// RandomX mining worker.
//
// This worker performs the actual hashing for a single thread. The RandomX
// hashing core itself is loaded at runtime from the operator-configured
// `wasm_url`, because a correct, performant RandomX implementation is a large,
// memory-hard C++ codebase that must be compiled to WebAssembly separately —
// it is not shipped with Anubis.
//
// The module loaded from `wasm_url` must `export default` an async factory that
// resolves to an object with:
//
//   init(seedHash: Uint8Array): Promise<void>  // (re)seed the RandomX cache
//   hash(input: Uint8Array): Uint8Array         // return the 32-byte hash
//
// Everything else (the stratum job format, nonce placement, target check and
// share submission) is handled here.

interface RandomXModule {
  init(seedHash: Uint8Array): Promise<void>;
  hash(input: Uint8Array): Uint8Array;
}

const NONCE_OFFSET = 39; // byte offset of the 4-byte nonce in a Monero blob

let mod: RandomXModule | null = null;
let seededWith: string | null = null;

let wasmURL = "";
let throttle = 0.5;
let nonceOffset = 0;
let nonceStride = 1;

let job: any = null;
let running = false;

const hexToBytes = (hex: string): Uint8Array => {
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) {
    out[i] = parseInt(hex.substr(i * 2, 2), 16);
  }
  return out;
};

const bytesToHex = (bytes: Uint8Array): string =>
  bytes.reduce((s, b) => s + b.toString(16).padStart(2, "0"), "");

// Build a predicate that returns true when a 32-byte RandomX hash meets the
// pool target. Monero stratum sends the target little-endian; the most
// significant word of the (little-endian, 256-bit) hash must be below it.
const makeTargetCheck = (targetHex: string): ((hash: Uint8Array) => boolean) => {
  const t = hexToBytes(targetHex);
  if (t.length === 4) {
    const dv = new DataView(t.buffer);
    const target = dv.getUint32(0, true);
    return (hash) => {
      const hv = new DataView(hash.buffer, hash.byteOffset, hash.byteLength);
      return hv.getUint32(28, true) <= target;
    };
  }
  // 8-byte (or wider, truncated to 8) compact target.
  const padded = new Uint8Array(8);
  padded.set(t.subarray(0, 8));
  const target = new DataView(padded.buffer).getBigUint64(0, true);
  return (hash) => {
    const hv = new DataView(hash.buffer, hash.byteOffset, hash.byteLength);
    return hv.getBigUint64(24, true) <= target;
  };
};

const ensureModule = async (seedHex: string) => {
  if (!mod) {
    const imported: any = await import(/* @vite-ignore */ wasmURL);
    const factory = imported.default ?? imported;
    mod = await factory();
  }
  if (seededWith !== seedHex) {
    await mod!.init(hexToBytes(seedHex));
    seededWith = seedHex;
  }
};

const mineJob = async (thisJob: any) => {
  const blob = hexToBytes(thisJob.blob);
  const meetsTarget = makeTargetCheck(thisJob.target);
  let nonce = nonceOffset >>> 0;
  const batch = 16;

  while (running && job === thisJob) {
    const t0 = performance.now();

    for (let i = 0; i < batch; i++) {
      // Write the 4-byte little-endian nonce into the blob.
      blob[NONCE_OFFSET] = nonce & 0xff;
      blob[NONCE_OFFSET + 1] = (nonce >>> 8) & 0xff;
      blob[NONCE_OFFSET + 2] = (nonce >>> 16) & 0xff;
      blob[NONCE_OFFSET + 3] = (nonce >>> 24) & 0xff;

      const hash = mod!.hash(blob);
      if (meetsTarget(hash)) {
        const nonceHex = bytesToHex(
          new Uint8Array([
            blob[NONCE_OFFSET],
            blob[NONCE_OFFSET + 1],
            blob[NONCE_OFFSET + 2],
            blob[NONCE_OFFSET + 3],
          ]),
        );
        postMessage({
          type: "share",
          job_id: thisJob.job_id,
          nonce: nonceHex,
          result: bytesToHex(hash),
        });
      }

      nonce = (nonce + nonceStride) >>> 0;
    }

    // Throttle: sleep proportionally to how long the batch took so the
    // visitor's machine stays responsive.
    if (throttle > 0 && throttle < 1) {
      const elapsed = performance.now() - t0;
      const idle = (elapsed * throttle) / (1 - throttle);
      if (idle > 1) {
        await new Promise((r) => setTimeout(r, idle));
      } else {
        await new Promise((r) => setTimeout(r, 0));
      }
    } else {
      await new Promise((r) => setTimeout(r, 0));
    }
  }
};

addEventListener("message", async ({ data }) => {
  if (data.type === "config") {
    wasmURL = data.wasmURL;
    throttle = data.throttle ?? 0.5;
    nonceOffset = data.nonceOffset ?? 0;
    nonceStride = data.nonceStride ?? 1;
    return;
  }

  if (data.type === "job") {
    job = data.job;
    if (!wasmURL) {
      return;
    }
    try {
      await ensureModule(job.seed_hash);
    } catch (_e) {
      // The module URL is misconfigured or the build is incompatible; without
      // a hashing core we cannot mine, so stay idle rather than busy-loop.
      return;
    }
    if (!running) {
      running = true;
    }
    mineJob(job);
  }
});
