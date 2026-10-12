package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// rawStream serves the given SSE data payloads, one per chunk.
func rawStream(t *testing.T, chunks []string, hang time.Duration) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			w.(http.Flusher).Flush()
		}
		if hang > 0 {
			select {
			case <-time.After(hang):
			case <-r.Context().Done():
			}
			return
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func content(s string) string {
	return fmt.Sprintf(`{"choices":[{"delta":{"content":%q}}]}`, s)
}

func TestStreamReasoningField(t *testing.T) {
	url := rawStream(t, []string{
		`{"choices":[{"delta":{"reasoning_content":"hmm, "}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"let me think"}}]}`,
		content("Answer."),
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"bash","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":\"ls\"}"}}]}}]}`,
	}, 0)
	var reasoning, answer string
	var started []string
	res, err := streamChat(context.Background(), url, "", "m", nil, nil, streamHooks{
		Content:   func(s string) { answer += s },
		Reasoning: func(s string) { reasoning += s },
		ToolStart: func(_ int, name string) { started = append(started, name) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if reasoning != "hmm, let me think" || answer != "Answer." || res.Content != "Answer." {
		t.Fatalf("reasoning %q answer %q content %q", reasoning, answer, res.Content)
	}
	if len(started) != 1 || started[0] != "bash" || res.ToolCalls[0].Function.Arguments != `{"command":"ls"}` {
		t.Fatalf("started %v calls %+v", started, res.ToolCalls)
	}
}

func TestStreamInlineThinkTags(t *testing.T) {
	// Tags split across chunks, and a "<" that isn't a tag.
	url := rawStream(t, []string{
		content("<thi"), content("nk>step one, "), content("step two</th"), content("ink>\n\nx < y is "), content("true"),
	}, 0)
	var reasoning, answer string
	res, err := streamChat(context.Background(), url, "", "m", nil, nil, streamHooks{
		Content:   func(s string) { answer += s },
		Reasoning: func(s string) { reasoning += s },
	})
	if err != nil {
		t.Fatal(err)
	}
	if reasoning != "step one, step two" {
		t.Fatalf("reasoning = %q", reasoning)
	}
	if res.Content != "x < y is true" || !strings.HasSuffix(answer, "x < y is true") {
		t.Fatalf("content = %q, streamed answer = %q", res.Content, answer)
	}
}

func TestStreamIdleTimeout(t *testing.T) {
	old := streamIdleTimeout
	streamIdleTimeout = 200 * time.Millisecond
	t.Cleanup(func() { streamIdleTimeout = old })

	url := rawStream(t, []string{content("partial")}, 10*time.Second)
	start := time.Now()
	_, err := streamChat(context.Background(), url, "", "m", nil, nil, streamHooks{})
	if !errors.Is(err, errStreamIdle) {
		t.Fatalf("err = %v, want errStreamIdle", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("took %s to give up", time.Since(start))
	}
}

func TestStreamCancelIsNotIdle(t *testing.T) {
	url := rawStream(t, []string{content("partial")}, 10*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	_, err := streamChat(ctx, url, "", "m", nil, nil, streamHooks{})
	if err == nil || errors.Is(err, errStreamIdle) || ctx.Err() == nil {
		t.Fatalf("err = %v", err)
	}
}

func TestApiPayloadMessagesPlainText(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: strPtr("sys")},
		{Role: "user", Content: strPtr("hi")},
	}
	out := apiPayloadMessages(msgs)
	if out[0]["content"] != "sys" || out[1]["content"] != "hi" {
		t.Fatalf("%#v", out)
	}
	if _, ok := out[1]["images"]; ok {
		t.Fatal("images key leaked into the wire payload")
	}
}

func TestApiPayloadMessagesWithImages(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: strPtr("what's this?"), Images: []string{"data:image/png;base64,AA==", "data:image/png;base64,BB=="}},
	}
	out := apiPayloadMessages(msgs)
	parts, ok := out[0]["content"].([]apiContentPart)
	if !ok || len(parts) != 3 {
		t.Fatalf("%#v", out[0]["content"])
	}
	if parts[0].Type != "text" || parts[0].Text != "what's this?" {
		t.Fatalf("text part: %#v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL.URL != "data:image/png;base64,AA==" {
		t.Fatalf("image part 1: %#v", parts[1])
	}
	if parts[2].ImageURL.URL != "data:image/png;base64,BB==" {
		t.Fatalf("image part 2: %#v", parts[2])
	}
}

func TestApiPayloadMessagesImagesOnlyNoText(t *testing.T) {
	msgs := []Message{{Role: "user", Content: nil, Images: []string{"data:image/png;base64,AA=="}}}
	out := apiPayloadMessages(msgs)
	parts := out[0]["content"].([]apiContentPart)
	if len(parts) != 1 || parts[0].Type != "image_url" {
		t.Fatalf("%#v", parts)
	}
}

func TestStreamChatSendsAuthorizationHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	streamChat(context.Background(), srv.URL, "secret-key", "m", nil, nil, streamHooks{})
	if gotAuth != "Bearer secret-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret-key")
	}
}

func TestStreamChatOmitsAuthorizationHeaderWhenNoKey(t *testing.T) {
	var sawHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHeader = r.Header.Get("Authorization") != ""
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	streamChat(context.Background(), srv.URL, "", "m", nil, nil, streamHooks{})
	if sawHeader {
		t.Error("Authorization header sent with an empty apiKey")
	}
}

func TestStreamChatSendsDefaultMaxTokens(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	streamChat(context.Background(), srv.URL, "", "m", nil, nil, streamHooks{})
	got, ok := gotBody["max_tokens"].(float64)
	if !ok || int(got) != defaultMaxTokens {
		t.Errorf("max_tokens = %#v, want %d", gotBody["max_tokens"], defaultMaxTokens)
	}
}
