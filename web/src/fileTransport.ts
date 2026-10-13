export const FILE_IDLE_TIMEOUT_MS = 60_000;
export const UPLOAD_CHUNK_BYTES = 64 * 1024;
export const UPLOAD_HIGH_WATER_BYTES = 1024 * 1024;
// Downloads are buffered for a Blob, not streamed to disk. Bound memory use.
export const MAX_BUFFERED_DOWNLOAD_BYTES = 128 * 1024 * 1024;

type FileReply = { type: string; error?: string };
type SocketOptions = { signal: AbortSignal; timeoutMs?: number };
type Operation<T> = {
  resolve: (value: T) => void;
  reject: (error: unknown) => void;
  touch: () => void;
  active: () => boolean;
};

/** Every socket promise settles exactly once and releases handlers/resources. */
export function socketOperation<T>(
  ws: WebSocket,
  options: SocketOptions,
  setup: (operation: Operation<T>) => void,
): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    let settled = false;
    let timer: ReturnType<typeof setTimeout>;
    const finish = (error: unknown, value?: T) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      options.signal.removeEventListener("abort", abort);
      ws.onopen = ws.onmessage = ws.onerror = ws.onclose = null;
      try { ws.close(); } catch { /* Settlement must not depend on close succeeding. */ }
      if (error) reject(error);
      else resolve(value as T);
    };
    const abort = () => finish(new DOMException("File operation cancelled", "AbortError"));
    const touch = () => {
      clearTimeout(timer);
      if (!settled) timer = setTimeout(() => finish(new Error("File operation timed out. Please retry.")), options.timeoutMs ?? FILE_IDLE_TIMEOUT_MS);
    };
    options.signal.addEventListener("abort", abort, { once: true });
    ws.onerror = () => finish(new Error("File connection failed. Please retry."));
    ws.onclose = () => finish(new Error("File connection closed before completion. Please retry."));
    if (options.signal.aborted) { abort(); return; }
    touch();
    try {
      setup({ resolve: (value) => finish(null, value), reject: (error) => finish(error), touch, active: () => !settled });
    } catch (error) { finish(error); }
  });
}

export function requestFiles<T extends FileReply>(ws: WebSocket, request: object, options: SocketOptions): Promise<T> {
  return socketOperation<T>(ws, options, ({ resolve, reject }) => {
    ws.onopen = () => { try { ws.send(JSON.stringify(request)); } catch (error) { reject(error); } };
    ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data) as T;
        if (msg.type === "file:error") reject(new Error(msg.error || "File operation failed"));
        else if (["file:list:result", "file:roots:result", "file:done"].includes(msg.type)) resolve(msg);
      } catch { reject(new Error("Invalid file response")); }
    };
  });
}

const pause = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

/** Blob slices avoid a whole-file copy; limit the WebSocket send queue too. */
export async function sendUploadChunks(ws: Pick<WebSocket, "readyState" | "bufferedAmount" | "send">, file: Blob, active: () => boolean, progress: () => void) {
  const check = () => {
    if (!active() || ws.readyState !== 1) throw new Error("Upload cancelled or connection closed");
  };
  const drain = async () => {
    while (ws.bufferedAmount > UPLOAD_HIGH_WATER_BYTES) {
      check();
      await pause(20);
    }
    check();
  };
  for (let offset = 0; offset < file.size; offset += UPLOAD_CHUNK_BYTES) {
    await drain();
    const chunk = await file.slice(offset, offset + UPLOAD_CHUNK_BYTES).arrayBuffer();
    check();
    ws.send(chunk);
    progress();
  }
  await drain();
  ws.send(JSON.stringify({ type: "file:upload:done" }));
}

export function uploadFile(ws: WebSocket, file: Blob, options: SocketOptions): Promise<void> {
  return socketOperation<void>(ws, options, ({ resolve, reject, active, touch }) => {
    ws.onopen = () => { void sendUploadChunks(ws, file, active, touch).catch(reject); };
    ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data) as FileReply;
        if (msg.type === "file:done") resolve();
        if (msg.type === "file:error") reject(new Error(msg.error || "Upload failed"));
      } catch { reject(new Error("Invalid upload response")); }
    };
  });
}

export function downloadFile(ws: WebSocket, options: SocketOptions): Promise<Blob> {
  ws.binaryType = "arraybuffer";
  const chunks: ArrayBuffer[] = [];
  let size = 0;
  return socketOperation<Blob>(ws, options, ({ resolve, reject, touch }) => {
    ws.onmessage = (event) => {
      try {
        touch();
        if (typeof event.data === "string") {
          const msg = JSON.parse(event.data) as FileReply;
          if (msg.type === "file:done") resolve(new Blob(chunks));
          if (msg.type === "file:error") reject(new Error(msg.error || "Download failed"));
        } else if (event.data instanceof ArrayBuffer) {
          size += event.data.byteLength;
          if (size > MAX_BUFFERED_DOWNLOAD_BYTES) {
            reject(new Error("Download exceeds the 128 MiB browser memory limit. Use another file transfer tool for larger files."));
          } else chunks.push(event.data);
        } else reject(new Error("Invalid download response"));
      } catch { reject(new Error("Invalid download response")); }
    };
  }).finally(() => { chunks.length = 0; });
}
