package protocol

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

const fixtureID = "00112233445566778899aabbccddeeff"

func textFrame(t testing.TB, text string) Frame {
	t.Helper()
	id, err := ParseMessageID(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	return Frame{Type: TypeText, ID: id, Text: text}
}

func TestGoldenFrames(t *testing.T) {
	text := textFrame(t, "Привет 👋\n<hello> & \"quote\"\\\t\u2028\u2029")
	for _, tt := range []struct {
		frame Frame
		body  string
	}{
		{text, `{"version":1,"type":"text","id":"00112233445566778899aabbccddeeff","text":"Привет 👋\n\u003chello\u003e \u0026 \"quote\"\\\t\u2028\u2029"}`},
		{Frame{Type: TypeAck, ID: text.ID}, `{"version":1,"type":"ack","id":"00112233445566778899aabbccddeeff"}`},
	} {
		encoded, err := encode(tt.frame)
		if err != nil || string(encoded) != tt.body {
			t.Fatalf("wire mismatch: %q, %v", encoded, err)
		}
		decoded, err := decode([]byte(tt.body))
		if err != nil || decoded != tt.frame {
			t.Fatalf("decode: %+v, %v", decoded, err)
		}
	}
}

func TestFrameValidationAndTextByteLimits(t *testing.T) {
	for _, text := range []string{" ", "\x00\x1b[31m\x7f\u0085", "�", strings.Repeat("a", MaxTextBytes), strings.Repeat("я", MaxTextBytes/2), strings.Repeat("\x00", MaxTextBytes)} {
		frame := textFrame(t, text)
		body, err := encode(frame)
		if err != nil {
			t.Fatal(err)
		}
		if len(body) > MaxFrameBytes {
			t.Fatal("maximum text cannot fit a frame")
		}
		got, err := decode(body)
		if err != nil || got != frame {
			t.Fatalf("boundary round trip: %v", err)
		}
	}
	for _, tt := range []struct {
		name  string
		frame Frame
		want  error
	}{
		{"empty", textFrame(t, ""), ErrInvalidText},
		{"large", textFrame(t, strings.Repeat("x", MaxTextBytes+1)), ErrInvalidText},
		{"large-utf8", textFrame(t, strings.Repeat("я", MaxTextBytes/2+1)), ErrInvalidText},
		{"invalid-utf8", textFrame(t, string([]byte{0xff})), ErrInvalidText},
		{"zero-id", Frame{Type: TypeText, Text: "hello"}, ErrInvalidID},
		{"unknown-type", Frame{Type: "future", ID: textFrame(t, "a").ID}, ErrUnsupportedType},
		{"ack-with-text", Frame{Type: TypeAck, ID: textFrame(t, "a").ID, Text: "hello"}, ErrInvalidFrame},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if body, err := encode(tt.frame); body != nil || !errors.Is(err, tt.want) {
				t.Fatalf("encode: %q, %v", body, err)
			}
		})
	}
}

func TestDecodeRejectsAmbiguousAndUnsupportedJSON(t *testing.T) {
	valid := `{"version":1,"type":"text","id":"` + fixtureID + `","text":"hello"}`
	ack := `{"version":1,"type":"ack","id":"` + fixtureID + `"}`
	for name, body := range map[string]string{
		"empty": "", "null": "null", "array": "[]", "broken": "{", "bom": "\ufeff" + valid,
		"leading-space": " " + valid, "trailing-newline": valid + "\n", "two-values": valid + ack,
		"missing-version":    strings.Replace(valid, `"version":1,`, "", 1),
		"wrong-order":        `{"type":"text","version":1,"id":"` + fixtureID + `","text":"hello"}`,
		"case-folded":        strings.Replace(valid, `"text":"hello"`, `"Text":"hello"`, 1),
		"escaped-key":        strings.Replace(valid, `"text":"hello"`, `"\u0074ext":"hello"`, 1),
		"escaped-text":       strings.Replace(valid, `"hello"`, `"\u0068ello"`, 1),
		"duplicate-version":  strings.Replace(valid, `"version":1`, `"version":2,"version":1`, 1),
		"duplicate-id":       strings.Replace(valid, `"id":`, `"id":"11111111111111111111111111111111","id":`, 1),
		"duplicate-text":     strings.Replace(valid, `"text":"hello"`, `"text":"hidden","text":"hello"`, 1),
		"unknown-field":      strings.Replace(valid, `"text":"hello"`, `"sender":"someone","text":"hello"`, 1),
		"missing-text":       ack[:len(ack)-1] + `,"type":"text"}`,
		"null-text":          strings.Replace(valid, `"hello"`, `null`, 1),
		"null-ack-text":      ack[:len(ack)-1] + `,"text":null}`,
		"empty-ack-text":     ack[:len(ack)-1] + `,"text":""}`,
		"ack-payload":        ack[:len(ack)-1] + `,"text":"hello"}`,
		"surrogate":          strings.Replace(valid, `"hello"`, `"\ud800"`, 1),
		"surrogate-pair":     strings.Replace(valid, `"hello"`, `"\ud83d\udc4b"`, 1),
		"bad-utf8":           strings.Replace(valid, "hello", string([]byte{0xff}), 1),
		"zero-id":            strings.Replace(valid, fixtureID, strings.Repeat("0", 32), 1),
		"upper-id":           strings.Replace(valid, fixtureID, strings.ToUpper(fixtureID), 1),
		"fractional-version": strings.Replace(valid, `"version":1`, `"version":1.0`, 1),
		"exponent-version":   strings.Replace(valid, `"version":1`, `"version":1e0`, 1),
		"overflow-version":   strings.Replace(valid, `"version":1`, `"version":184467440737095516160`, 1),
		"oversize-text":      strings.Replace(valid, "hello", strings.Repeat("x", MaxTextBytes+1), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if frame, err := decode([]byte(body)); frame != (Frame{}) || !errors.Is(err, ErrInvalidFrame) {
				t.Fatalf("accepted invalid body: %+v, %v", frame, err)
			}
		})
	}
	for _, tt := range []struct {
		body string
		want error
	}{
		{strings.Replace(valid, `"version":1`, `"version":2`, 1), ErrUnsupportedVersion},
		{strings.Replace(valid, `"type":"text"`, `"type":"future"`, 1), ErrUnsupportedType},
		{strings.Repeat("x", MaxFrameBytes+1), ErrFrameTooLarge},
	} {
		if frame, err := decode([]byte(tt.body)); frame != (Frame{}) || !errors.Is(err, tt.want) {
			t.Fatal(err)
		}
	}
}

func TestProtocolErrorsDoNotEchoMessageContent(t *testing.T) {
	marker := "private-message-marker"
	for _, body := range []string{
		`{"version":"` + marker + `"}`,
		`{"version":1,"type":"` + marker + `","id":"` + fixtureID + `"}`,
		`{"version":1,"type":"text","id":"` + marker + `","text":"hello"}`,
		`{"version":1,"type":"text","id":"` + fixtureID + `","text":["` + marker + `"]}`,
	} {
		_, err := decode([]byte(body))
		if err == nil || strings.Contains(err.Error(), marker) {
			t.Fatalf("unsafe parser error: %v", err)
		}
	}
}

func TestMessageIDGenerationAndStrictParsing(t *testing.T) {
	seen := make(map[MessageID]bool)
	for range 128 {
		id, err := NewMessageID()
		if err != nil || id == (MessageID{}) || seen[id] {
			t.Fatalf("invalid generated ID: %v", err)
		}
		seen[id] = true
		parsed, err := ParseMessageID(id.String())
		if err != nil || parsed != id {
			t.Fatalf("ID round trip: %v", err)
		}
	}
	for _, value := range []string{"", "0", fixtureID[:31], fixtureID + "0", strings.Repeat("0", 32), strings.ToUpper(fixtureID), " " + fixtureID, strings.Repeat("g", 32), strings.Repeat("я", 16)} {
		if id, err := ParseMessageID(value); id != (MessageID{}) || !errors.Is(err, ErrInvalidID) {
			t.Fatalf("accepted %q: %v", value, err)
		}
	}
}

func FuzzDecodeFrame(f *testing.F) {
	body, _ := encode(textFrame(f, "Привет\nworld"))
	ack, _ := encode(Frame{Type: TypeAck, ID: textFrame(f, "a").ID})
	for _, seed := range [][]byte{body, ack, nil, []byte("{}"), []byte(`{"version":1,"text":"\ud800"}`)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		frame, err := decode(input)
		if err != nil {
			if frame != (Frame{}) {
				t.Fatal("failure returned application data")
			}
			return
		}
		canonical, err := encode(frame)
		if err != nil || !bytes.Equal(input, canonical) {
			t.Fatal("accepted noncanonical or unwritable frame")
		}
	})
}
