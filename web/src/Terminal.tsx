import { useEffect, useRef } from "react";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal as XTerm } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";

// A real, unrestricted shell in the session's project directory - see
// terminal.go. It bypasses the agent's tool-call approval system entirely;
// this panel is a terminal, not another way to talk to the agent. One shell
// per mount: unmounting (closing the panel, switching sessions) kills it,
// and remounting starts a fresh one - nothing here is resumable.
export function TerminalPanel({ sessionId }: { sessionId: string }) {
  const hostRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;

    // xterm.js renders via canvas, and canvas font strings can't resolve
    // CSS custom properties like var(--mono) - it silently falls back to
    // the browser's default monospace. Resolve the real value instead.
    const mono = getComputedStyle(document.documentElement).getPropertyValue("--mono").trim() || "monospace";

    const term = new XTerm({
      convertEol: true,
      cursorBlink: true,
      fontSize: 13,
      fontFamily: mono,
      theme: {
        background: "#00000000",
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    fit.fit();

    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(`${proto}//${location.host}/api/sessions/${encodeURIComponent(sessionId)}/terminal`);
    ws.binaryType = "arraybuffer";

    const sendResize = () => {
      fit.fit();
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
      }
    };

    ws.onopen = sendResize;
    // fit() right after mount can measure the wrong cell size if the
    // monospace web font hasn't finished loading yet, locking in too few
    // rows/cols for the panel's real size. Font loading doesn't resize the
    // container, so ResizeObserver below never catches it either - it has
    // to be corrected explicitly once the font is confirmed ready.
    document.fonts.ready.then(sendResize);
    ws.onmessage = (e: MessageEvent) => {
      if (typeof e.data === "string") term.write(e.data);
      else term.write(new Uint8Array(e.data));
    };
    ws.onclose = () => term.write("\r\n\x1b[90m[disconnected]\x1b[0m\r\n");

    const dataSub = term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(data);
    });

    const resizeObserver = new ResizeObserver(sendResize);
    resizeObserver.observe(host);

    return () => {
      resizeObserver.disconnect();
      dataSub.dispose();
      ws.close();
      term.dispose();
    };
  }, [sessionId]);

  return <div className="terminal-host" ref={hostRef} />;
}
