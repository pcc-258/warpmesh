/* global AbortController, Blob, DOMException, setTimeout */
import test from "node:test";
import assert from "node:assert/strict";
import { canFallback, createDeadline, negotiateOffer, refreshFailure, terminalOutputIsLive } from "./sessionLifecycle.ts";
import { downloadFile, requestFiles, sendUploadChunks, uploadFile, UPLOAD_CHUNK_BYTES, UPLOAD_HIGH_WATER_BYTES, MAX_BUFFERED_DOWNLOAD_BYTES } from "./fileTransport.ts";

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
class FakeSocket {
  readyState = 1;
  bufferedAmount = 0;
  sent = [];
  closes = 0;
  send(data) { this.sent.push(data); }
  close() { this.closes++; this.readyState = 3; this.onclose?.(); }
  message(data) { this.onmessage?.({ data }); }
}
const options = () => ({ signal: new AbortController().signal, timeoutMs: 10 });

test("refresh expires authentication only on 401; network/5xx/403 preserve view", () => {
  assert.equal(refreshFailure({ status: 401 }).logout, true);
  for (const error of [new TypeError("offline"), { status: 500 }, { status: 503 }, { status: 403 }]) {
    const result = refreshFailure(error);
    assert.equal(result.logout, false);
    assert.match(result.message, /preserved.*retry/i);
  }
});

test("both async offer failure paths trigger fallback, closed peers do not", async () => {
  for (const step of ["createOffer", "setLocalDescription"]) {
    let failures = 0;
    const peer = { signalingState: "stable", createOffer: async () => ({}), setLocalDescription: async () => {}, localDescription: {} };
    peer[step] = async () => { throw new Error(step); };
    await negotiateOffer(peer, () => assert.fail("must not send"), () => failures++);
    assert.equal(failures, 1);
    peer.signalingState = "closed";
    await negotiateOffer(peer, () => assert.fail("must not send"), () => failures++);
    assert.equal(failures, 1);
  }
});

test("deadline fires without an offer and cancels after live/disposal", async () => {
  let expired = 0;
  const deadline = createDeadline();
  deadline.arm(5, () => expired++);
  await delay(15);
  assert.equal(expired, 1);
  deadline.arm(5, () => expired++);
  deadline.cancel();
  await delay(15);
  assert.equal(expired, 1);
  deadline.arm(5, () => expired++);
  deadline.dispose();
  deadline.arm(5, () => expired++);
  await delay(15);
  assert.equal(expired, 1);
});

test("early terminal welcome cannot cancel a stuck offer budget; late offers stay closed", async () => {
  let resolveOffer;
  let fallbacks = 0;
  const peer = {
    signalingState: "stable",
    createOffer: () => new Promise((resolve) => { resolveOffer = resolve; }),
    setLocalDescription: () => assert.fail("closed peer must not continue negotiation"),
    localDescription: {},
  };
  const deadline = createDeadline();
  deadline.arm(5, () => { fallbacks++; peer.signalingState = "closed"; });
  const negotiating = negotiateOffer(peer, () => assert.fail("stale offer sent"), () => assert.fail("unexpected failure"));
  if (terminalOutputIsLive(false, false)) deadline.cancel();
  await delay(15);
  assert.equal(fallbacks, 1);
  resolveOffer({});
  await negotiating;
  assert.equal(terminalOutputIsLive(true, false), true);
  assert.equal(terminalOutputIsLive(false, true), true);
});

test("VNC/relay handshake has its own deadline and cancellation", async () => {
  const handshake = createDeadline();
  let failures = 0;
  handshake.arm(5, () => failures++);
  await delay(15);
  assert.equal(failures, 1);
  handshake.arm(5, () => failures++);
  handshake.cancel(); // actual VNC connect
  await delay(15);
  assert.equal(failures, 1);
});

test("socket completion stays settled even when close throws", async () => {
  const ws = new FakeSocket();
  ws.close = () => { throw new Error("close failure"); };
  const reply = requestFiles(ws, { type: "file:list" }, options());
  ws.message(JSON.stringify({ type: "file:list:result", entries: [] }));
  assert.deepEqual((await reply).entries, []);
  const failed = new FakeSocket();
  failed.close = ws.close;
  const promise = requestFiles(failed, {}, options());
  failed.onclose();
  await assert.rejects(promise, /closed before completion/);
});

test("stale timeout cannot disrupt live direct, but real failure can recover once", () => {
  assert.equal(canFallback(false, false, true, false), false);
  assert.equal(canFallback(false, false, true, true), true);
  assert.equal(canFallback(false, true, true, true), false);
  assert.equal(canFallback(true, false, false, true), false);
});

for (const operation of [
  (ws, opts) => requestFiles(ws, { type: "file:list" }, opts),
  (ws, opts) => uploadFile(ws, new Blob(["hello"]), opts),
  (ws, opts) => downloadFile(ws, opts),
]) {
  test(`${operation.toString().match(/=> (\w+)/)[1]} rejects on premature close, timeout, abort and invalid JSON`, async () => {
    for (const failure of ["close", "timeout", "abort", "json", "error"]) {
      const ws = new FakeSocket();
      const controller = new AbortController();
      const promise = operation(ws, { signal: controller.signal, timeoutMs: 5 });
      const rejected = assert.rejects(promise, failure === "abort" ? { name: "AbortError" } : /closed|timed out|Invalid|connection failed/);
      if (failure === "close") ws.onclose();
      if (failure === "abort") controller.abort();
      if (failure === "json") ws.message("not JSON");
      if (failure === "error") ws.onerror();
      await rejected;
      assert.equal(ws.closes, 1);
      assert.equal(ws.onmessage, null);
      assert.equal(ws.onclose, null);
    }
  });
}

test("list and upload settle successfully and close socket exactly once", async () => {
  const ws = new FakeSocket();
  const result = requestFiles(ws, { type: "file:list" }, options());
  ws.onopen();
  ws.message(JSON.stringify({ type: "file:list:result", entries: [] }));
  assert.deepEqual((await result).entries, []);
  assert.equal(ws.closes, 1);
  const upload = new FakeSocket();
  const done = uploadFile(upload, new Blob(), options());
  upload.message(JSON.stringify({ type: "file:done" }));
  await done;
  assert.equal(upload.closes, 1);
});

test("upload slices at 64KiB without reading entire file; sends completion last", async () => {
  const file = new Blob([new ArrayBuffer(UPLOAD_CHUNK_BYTES * 2 + 17)]);
  file.arrayBuffer = () => assert.fail("whole file read");
  const ws = new FakeSocket();
  await sendUploadChunks(ws, file, () => true, () => {});
  assert.deepEqual(ws.sent.slice(0, -1).map((chunk) => chunk.byteLength), [UPLOAD_CHUNK_BYTES, UPLOAD_CHUNK_BYTES, 17]);
  assert.equal(JSON.parse(ws.sent.at(-1)).type, "file:upload:done");
});

test("upload backpressure waits, then resumes; cancellation stops queued sends", async () => {
  const ws = new FakeSocket();
  ws.bufferedAmount = UPLOAD_HIGH_WATER_BYTES + 1;
  let active = true;
  const sending = sendUploadChunks(ws, new Blob(["hello"]), () => active, () => {});
  await delay(5);
  assert.equal(ws.sent.length, 0);
  ws.bufferedAmount = 0;
  await sending;
  assert.equal(ws.sent.length, 2);
  const blocked = new FakeSocket();
  blocked.bufferedAmount = UPLOAD_HIGH_WATER_BYTES + 1;
  const cancelled = sendUploadChunks(blocked, new Blob(["hello"]), () => active, () => {});
  active = false;
  await assert.rejects(cancelled, /cancelled/);
  assert.equal(blocked.sent.length, 0);
});

test("upload read failure and blocked send timeout reject without leaking the socket", async () => {
  const ws = new FakeSocket();
  const unreadable = { size: 1, slice: () => ({ arrayBuffer: async () => { throw new Error("read failed"); } }) };
  const read = uploadFile(ws, unreadable, options());
  ws.onopen();
  await assert.rejects(read, /read failed/);
  assert.equal(ws.closes, 1);
  const blocked = new FakeSocket();
  blocked.bufferedAmount = UPLOAD_HIGH_WATER_BYTES + 1;
  const stalled = uploadFile(blocked, new Blob(["hello"]), options());
  blocked.onopen();
  await assert.rejects(stalled, /timed out/);
  await delay(25); // allow the bounded backpressure poll to observe cancellation
  assert.equal(blocked.closes, 1);
  assert.equal(blocked.sent.length, 0);
});

test("download assembles bytes only on completion and rejects oversized buffering", async () => {
  const ws = new FakeSocket();
  const downloaded = downloadFile(ws, options());
  ws.message(new Uint8Array([1, 2, 3]).buffer);
  ws.message(JSON.stringify({ type: "file:done" }));
  assert.deepEqual(new Uint8Array(await (await downloaded).arrayBuffer()), new Uint8Array([1, 2, 3]));
  assert.equal(ws.closes, 1);
  const large = new FakeSocket();
  const oversized = downloadFile(large, options());
  const rejected = assert.rejects(oversized, /128 MiB/);
  // The fake repeats a single buffer: exercise the size guard cheaply.
  const chunk = new ArrayBuffer(1024 * 1024);
  for (let i = 0; i <= MAX_BUFFERED_DOWNLOAD_BYTES / chunk.byteLength; i++) large.message(chunk);
  await rejected;
  assert.equal(large.closes, 1);
});

test("pre-aborted unmount signal immediately closes socket", async () => {
  const controller = new AbortController();
  controller.abort(new DOMException("Unmount", "AbortError"));
  const ws = new FakeSocket();
  await assert.rejects(requestFiles(ws, {}, { signal: controller.signal }), { name: "AbortError" });
  assert.equal(ws.closes, 1);
});
