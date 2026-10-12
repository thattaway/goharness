import { describe, expect, it } from "vitest";
import type { Message } from "./api";
import { applyEvent, fromMessages } from "./transcript";

const u = (content: string): Message => ({ role: "user", content });
const a = (content: string): Message => ({ role: "assistant", content });

describe("turn ends", () => {
  it("closes each turn before the next user message and at the end", () => {
    const msgs = [u("edit"), a("done"), u("chat"), a("hi")];
    const items = fromMessages(msgs, [
      { turn: 1, msgIndex: 0, files: 2, hasHistory: true },
      { turn: 2, msgIndex: 2, files: 0, hasHistory: true },
    ]);
    expect(items.map((i) => i.kind)).toEqual(["user", "agent", "turnEnd", "user", "agent", "turnEnd"]);
    const ends = items.filter((i) => i.kind === "turnEnd");
    expect(ends.map((e) => [e.turn, e.files])).toEqual([
      [1, 2],
      [2, 0],
    ]);
  });

  it("works without turn info (older sessions)", () => {
    expect(fromMessages([u("x"), a("y")]).map((i) => i.kind)).toEqual(["user", "agent"]);
  });

  it("adds a turn end when a live run reports its changes", () => {
    let items = fromMessages([]);
    items = applyEvent(items, { type: "user", text: "edit" });
    items = applyEvent(items, { type: "delta", text: "ok" });
    items = applyEvent(items, { type: "assistant", text: "ok" });
    items = applyEvent(items, { type: "changes", turn: 3, files: 4 });
    items = applyEvent(items, { type: "done" });
    const last = items[items.length - 1];
    expect(last).toMatchObject({ kind: "turnEnd", turn: 3, files: 4, hasHistory: true });
  });
});
