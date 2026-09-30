import { useCallback, useEffect, useRef, useState } from "react";

/**
 * Connection stages a browser session moves through before pixels or shell
 * output arrive.
 *
 * The direct WebRTC attempt can legitimately take up to DIRECT_BUDGET_MS while
 * ICE gathers, connectivity checks run and DTLS/SCTP come up. Without a staged
 * report the UI shows one static label for that whole window, which is long
 * enough that the operator assumes the page has hung.
 */
export type SessionStage =
  | "setup"
  | "signaling"
  | "negotiating"
  | "checking"
  | "switching"
  | "relaying"
  | "connected"
  | "disconnected"
  | "error";

/** What the session is streaming, used to word the relay/connected detail. */
export type SessionKind = "terminal" | "desktop";

/** How long a direct path is attempted before falling back to the relay. */
export const DIRECT_BUDGET_MS = 20000;

export type StageAttempt = {
  stage: SessionStage;
  /** Milliseconds since the session screen opened. */
  elapsedMs: number;
  /** Milliseconds left in the direct attempt budget, when one is running. */
  budgetRemainingMs: number | null;
  /** ICE candidates gathered from our side so far. */
  localCandidates: number;
  /** ICE candidates received from the device so far. */
  remoteCandidates: number;
  /** Present when stage is "error" or "disconnected". */
  reason?: string;
};

/**
 * Tracks the connection stage of a browser session and ticks elapsed/remaining
 * time so the UI can show progress instead of a frozen label.
 *
 * Callers drive it from the WebSocket and RTCPeerConnection events via the
 * returned callbacks; the hook only owns the derived, human-facing state.
 */
export function useSessionStage() {
  const [stage, setStage] = useState<SessionStage>("setup");
  const [reason, setReason] = useState<string | undefined>(undefined);
  const [attempt, setAttempt] = useState<StageAttempt>({
    stage: "setup",
    elapsedMs: 0,
    budgetRemainingMs: null,
    localCandidates: 0,
    remoteCandidates: 0,
  });

  const stageRef = useRef<SessionStage>("setup");
  const reasonRef = useRef<string | undefined>(undefined);
  const startedAtRef = useRef<number>(Date.now());
  const budgetEndsAtRef = useRef<number | null>(null);
  const localCandidatesRef = useRef(0);
  const remoteCandidatesRef = useRef(0);
  // Re-render on a 250ms cadence: fine enough to look live, cheap enough to
  // leave running for the whole session.
  const [, forceTick] = useState(0);

  const applyStage = useCallback((next: SessionStage, why?: string) => {
    if (!shouldAdvanceStage(stageRef.current, next)) return;
    stageRef.current = next;
    setStage(next);
    if (why !== undefined) {
      reasonRef.current = why;
      setReason(why);
    }
  }, []);

  const begin = useCallback(() => {
    startedAtRef.current = Date.now();
    budgetEndsAtRef.current = null;
    localCandidatesRef.current = 0;
    remoteCandidatesRef.current = 0;
    applyStage("setup", undefined);
  }, [applyStage]);

  /** Start (or restart) the direct attempt budget shown as a countdown. */
  const startBudget = useCallback(
    (ms: number = DIRECT_BUDGET_MS) => {
      budgetEndsAtRef.current = Date.now() + ms;
      applyStage("checking");
    },
    [applyStage],
  );

  const stopBudget = useCallback(() => {
    budgetEndsAtRef.current = null;
  }, []);

  const noteLocalCandidate = useCallback(() => {
    localCandidatesRef.current += 1;
  }, []);

  const noteRemoteCandidate = useCallback(() => {
    remoteCandidatesRef.current += 1;
  }, []);

  const markRelaying = useCallback(() => {
    stopBudget();
    applyStage("relaying");
  }, [applyStage, stopBudget]);

  const markConnected = useCallback(() => {
    stopBudget();
    applyStage("connected", undefined);
  }, [applyStage, stopBudget]);

  const markFailed = useCallback(
    (why?: string) => {
      stopBudget();
      applyStage("error", why);
    },
    [applyStage, stopBudget],
  );

  const markDisconnected = useCallback(
    (why?: string) => {
      stopBudget();
      applyStage("disconnected", why);
    },
    [applyStage, stopBudget],
  );

  useEffect(() => {
    const id = window.setInterval(() => {
      const now = Date.now();
      const budget = budgetEndsAtRef.current;
      setAttempt({
        stage: stageRef.current,
        elapsedMs: now - startedAtRef.current,
        budgetRemainingMs: budget === null ? null : Math.max(0, budget - now),
        localCandidates: localCandidatesRef.current,
        remoteCandidates: remoteCandidatesRef.current,
        reason: reasonRef.current,
      });
      forceTick((n) => (n + 1) % 1000);
    }, 250);
    return () => window.clearInterval(id);
  }, []);

  return {
    stage,
    reason,
    attempt,
    begin,
    setStage: applyStage,
    startBudget,
    stopBudget,
    noteLocalCandidate,
    noteRemoteCandidate,
    markRelaying,
    markConnected,
    markFailed,
    markDisconnected,
  };
}

/** Human-readable label for a stage. */
export function stageLabel(stage: SessionStage): string {
  switch (stage) {
    case "setup":
      return "Preparing session";
    case "signaling":
      return "Contacting device";
    case "negotiating":
      return "Negotiating connection";
    case "checking":
      return "Trying direct connection";
    case "switching":
      return "Direct failed, switching to relay";
    case "relaying":
      return "Connecting through server relay";
    case "connected":
      return "Connected";
    case "disconnected":
      return "Disconnected";
    case "error":
      return "Connection failed";
  }
}

/**
 * Progress order of the handshake stages.
 *
 * Events arrive out of order: terminal or desktop output can land while ICE is
 * still probing, and RTCPeerConnection then reports "connecting"/"checking"
 * afterwards. Without an ordering guard that late event overwrites "connected"
 * with "checking" and the waiting overlay comes back over a live session.
 */
function stageRank(stage: SessionStage): number {
  switch (stage) {
    case "setup":
      return 0;
    case "signaling":
      return 1;
    case "negotiating":
      return 2;
    case "checking":
      return 3;
    case "switching":
      return 4;
    case "relaying":
      return 5;
    case "connected":
      return 6;
    // Terminal states are handled explicitly, never by rank.
    case "disconnected":
    case "error":
      return 99;
  }
}

const TERMINAL_STAGES: SessionStage[] = ["connected", "disconnected", "error"];

/**
 * Whether a stage transition should be applied.
 *
 * Events arrive out of order: session output can land while ICE is still
 * probing, and RTCPeerConnection then reports "connecting"/"checking"
 * afterwards. Without this guard that late event overwrites "connected" with
 * "checking" and the waiting overlay returns on top of a live session.
 *
 * Rules:
 *   - a terminal stage (connected/error/disconnected) may only be replaced by
 *     another terminal stage;
 *   - otherwise progress must not move backwards.
 */
export function shouldAdvanceStage(current: SessionStage, next: SessionStage): boolean {
  const currentTerminal = TERMINAL_STAGES.includes(current);
  if (currentTerminal) return TERMINAL_STAGES.includes(next);
  return stageRank(next) >= stageRank(current);
}

/** Second line of detail: what is happening right now and what happens next. */
export function stageDetail(attempt: StageAttempt, kind: SessionKind = "desktop"): string {
  const seconds = (ms: number) => `${Math.max(0, Math.round(ms / 1000))}s`;
  const candidates = `${attempt.localCandidates}/${attempt.remoteCandidates}`;
  const stream = kind === "terminal" ? "terminal" : "desktop";
  switch (attempt.stage) {
    case "setup":
      return "Opening the control channel to the relay.";
    case "signaling":
      return "Exchange started; waiting for the device to answer.";
    case "negotiating":
      return `Answer received, gathering network paths (candidates ${candidates}).`;
    case "checking":
      return attempt.budgetRemainingMs === null
        ? `Testing a direct path (candidates ${candidates}).`
        : `Testing a direct path (candidates ${candidates}). Falling back to the server relay in ${seconds(attempt.budgetRemainingMs)}.`;
    case "switching":
      return "Reconnecting through the server relay.";
    case "relaying":
      return `Relay selected; waiting for the ${stream} stream to start.`;
    case "connected":
      return "Streaming.";
    case "disconnected":
      return attempt.reason || "The session ended.";
    case "error":
      return attempt.reason || "Could not establish a session.";
  }
}

/**
 * Whether a stage is still in progress. Used to pick the running spinner and
 * the "elapsed" readout.
 */
export function stageIsPending(stage: SessionStage): boolean {
  return stage === "setup" || stage === "signaling" || stage === "negotiating" || stage === "checking" || stage === "switching" || stage === "relaying";
}

/** Short elapsed readout, e.g. "6s" or "1m 04s". */
export function formatElapsed(ms: number): string {
  const total = Math.max(0, Math.round(ms / 1000));
  if (total < 60) return `${total}s`;
  return `${Math.floor(total / 60)}m ${String(total % 60).padStart(2, "0")}s`;
}
