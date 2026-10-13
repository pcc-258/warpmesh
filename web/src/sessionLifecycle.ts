export const DIRECT_BUDGET_MS = 20_000;
export const HANDSHAKE_BUDGET_MS = 20_000;

/** Small shared deadline owner. Completion/disposal cancels stale callbacks. */
export function createDeadline() {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let disposed = false;
  const cancel = () => {
    clearTimeout(timer);
    timer = undefined;
  };
  return {
    arm(ms: number, expire: () => void) {
      cancel();
      if (disposed) return;
      timer = setTimeout(() => {
        timer = undefined;
        if (!disposed) expire();
      }, ms);
    },
    cancel,
    dispose() {
      disposed = true;
      cancel();
    },
  };
}

/** Recover an actual transport failure even after direct became live. */
export function canFallback(disposed: boolean, requested: boolean, live: boolean, failed: boolean) {
  return !disposed && !requested && (!live || failed);
}

/** Welcome output sent over the control channel is not proof of direct RTC. */
export function terminalOutputIsLive(directActive: boolean, relayActive: boolean) {
  return directActive || relayActive;
}

/** Observe both asynchronous negotiation failures without leaking rejections. */
export async function negotiateOffer(
  peer: Pick<RTCPeerConnection, "createOffer" | "setLocalDescription" | "localDescription" | "signalingState">,
  send: (description: RTCSessionDescription) => void,
  failed: () => void,
) {
  const closed = () => peer.signalingState === "closed";
  try {
    const offer = await peer.createOffer();
    if (closed()) return;
    await peer.setLocalDescription(offer);
    if (!closed() && peer.localDescription) send(peer.localDescription);
  } catch {
    if (!closed()) failed();
  }
}

export function refreshFailure(error: unknown): { logout: boolean; message: string } {
  const status = typeof error === "object" && error !== null && "status" in error ? error.status : undefined;
  return {
    logout: status === 401,
    message: typeof status === "number"
      ? `Unable to refresh devices (server returned ${status}). Your current view is preserved. Please retry.`
      : "Unable to reach the server. Your current view is preserved. Check your connection and retry.",
  };
}
