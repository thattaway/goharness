// Getting the user's attention when the agent needs them: a short chime,
// a vibration on phones, and a system notification when the page is in
// the background. Browsers only allow system notifications on HTTPS (or
// localhost), so on plain-HTTP LAN access the chime and the tab title are
// what's left.

export interface NotifyPrefs {
  sound: boolean;
  system: boolean;
}

const KEY = "goharness.notify";

export function getPrefs(): NotifyPrefs {
  try {
    const raw = localStorage.getItem(KEY);
    if (raw) return { sound: true, system: false, ...JSON.parse(raw) };
  } catch {
    /* storage unavailable */
  }
  return { sound: true, system: false };
}

export function setPrefs(p: NotifyPrefs) {
  try {
    localStorage.setItem(KEY, JSON.stringify(p));
  } catch {
    /* storage unavailable */
  }
}

export const systemNotificationsSupported = () => window.isSecureContext && "Notification" in window;

export async function enableSystemNotifications(): Promise<boolean> {
  if (!systemNotificationsSupported()) return false;
  const result = await Notification.requestPermission();
  return result === "granted";
}

let audio: AudioContext | null = null;

// Browsers only start audio after a user gesture, so warm it up on the
// first click or key press.
function unlockAudio() {
  try {
    audio ??= new AudioContext();
    if (audio.state === "suspended") audio.resume();
  } catch {
    /* no Web Audio */
  }
}
window.addEventListener("pointerdown", unlockAudio, { once: true });
window.addEventListener("keydown", unlockAudio, { once: true });

function chime() {
  if (!audio || audio.state !== "running") return;
  const now = audio.currentTime;
  for (const [i, freq] of [660, 880].entries()) {
    const osc = audio.createOscillator();
    const gain = audio.createGain();
    osc.frequency.value = freq;
    osc.type = "sine";
    const t = now + i * 0.14;
    gain.gain.setValueAtTime(0.0001, t);
    gain.gain.exponentialRampToValueAtTime(0.15, t + 0.02);
    gain.gain.exponentialRampToValueAtTime(0.0001, t + 0.25);
    osc.connect(gain).connect(audio.destination);
    osc.start(t);
    osc.stop(t + 0.3);
  }
}

export function alertUser(title: string, body: string, opts: { onlyIfHidden?: boolean } = {}) {
  if (opts.onlyIfHidden && !document.hidden) return;
  const prefs = getPrefs();
  if (prefs.sound) chime();
  navigator.vibrate?.(120);
  if (prefs.system && document.hidden && systemNotificationsSupported() && Notification.permission === "granted") {
    try {
      const n = new Notification(title, { body, tag: "goharness", icon: "/favicon.svg" });
      n.onclick = () => {
        window.focus();
        n.close();
      };
    } catch {
      /* some mobile browsers only allow notifications from a service worker */
    }
  }
}
