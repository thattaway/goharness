import { describe, expect, it } from "vitest";
import { joinDictation, pickMimeType, recordingName } from "./dictation";

describe("joinDictation", () => {
  it("adds spoken text after what is typed, with one space", () => {
    expect(joinDictation("Please look at", "the failing test")).toBe("Please look at the failing test");
    expect(joinDictation("Please look at  \n", "the failing test")).toBe("Please look at the failing test");
  });
  it("uses the spoken text alone when nothing is typed", () => {
    expect(joinDictation("", "  hello there ")).toBe("hello there");
    expect(joinDictation("   ", "hello")).toBe("hello");
  });
  it("leaves the text alone when nothing was heard", () => {
    expect(joinDictation("typed", "")).toBe("typed");
    expect(joinDictation("typed", "   ")).toBe("typed");
  });
});

describe("pickMimeType", () => {
  it("prefers webm/opus, then falls back", () => {
    expect(pickMimeType(() => true)).toBe("audio/webm;codecs=opus");
    expect(pickMimeType((t) => t === "audio/mp4")).toBe("audio/mp4");
    expect(pickMimeType((t) => t.startsWith("audio/ogg"))).toBe("audio/ogg;codecs=opus");
  });
  it("returns empty when nothing is supported so the browser default is used", () => {
    expect(pickMimeType(() => false)).toBe("");
  });
});

describe("recordingName", () => {
  it("names the file after its format", () => {
    expect(recordingName("audio/webm;codecs=opus")).toBe("dictation.webm");
    expect(recordingName("audio/mp4")).toBe("dictation.mp4");
    expect(recordingName("audio/ogg;codecs=opus")).toBe("dictation.ogg");
    expect(recordingName("")).toBe("dictation.webm");
  });
});
