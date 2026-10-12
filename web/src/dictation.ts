// Dictation: record speech in the browser and have the server turn it into text.

/** Appends spoken text to what is already typed, with a space between. */
export function joinDictation(existing: string, spoken: string): string {
  const s = spoken.trim();
  if (!s) return existing;
  if (!existing.trim()) return s;
  return existing.replace(/\s+$/, "") + " " + s;
}

/**
 * Whether this page can record from the microphone. Browsers only allow it on
 * HTTPS pages and on localhost, so a plain-http LAN address cannot.
 */
export function canRecord(): boolean {
  return (
    typeof window !== "undefined" &&
    window.isSecureContext &&
    typeof navigator !== "undefined" &&
    !!navigator.mediaDevices?.getUserMedia &&
    typeof MediaRecorder !== "undefined"
  );
}

/** The first recording format this browser supports that the server can read. */
export function pickMimeType(isSupported: (t: string) => boolean): string {
  for (const t of ["audio/webm;codecs=opus", "audio/webm", "audio/mp4", "audio/ogg;codecs=opus"]) {
    if (isSupported(t)) return t;
  }
  return "";
}

/** A file name with the right extension for a recording, so the server knows how to read it. */
export function recordingName(mime: string): string {
  if (mime.includes("mp4")) return "dictation.mp4";
  if (mime.includes("ogg")) return "dictation.ogg";
  return "dictation.webm";
}

export const insecureMicMessage =
  "This page isn't served over HTTPS, so the browser won't allow the microphone here. Pick or record an audio file instead, or open goharness through an HTTPS address.";
