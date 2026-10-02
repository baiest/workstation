package claude

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

const (
	headLines  = 50       // lines scanned from the start to find cwd
	tailBytes  = 64 << 10 // bytes read from the end for recent activity
	maxMessage = 160      // runes kept of the last assistant message
	maxLine    = 1 << 20  // longer transcript lines are skipped, not loaded
)

type transcriptInfo struct {
	Cwd          string
	Branch       string
	Slug         string // names the session's plan file: <root>/plans/<slug>.md
	LastActivity time.Time
	LastMessage  string
}

type record struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
	GitBranch string `json:"gitBranch"`
	Slug      string `json:"slug"`
	Message   *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// readTranscript extracts what we need from a session .jsonl without reading
// it whole (they reach several MB): the head gives the cwd, the tail gives
// last activity and last assistant message. Lines that fail to parse (e.g.
// one still being written) are skipped.
func readTranscript(path string) (transcriptInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return transcriptInfo{}, err
	}
	defer f.Close()

	var info transcriptInfo
	headCwd, headBranch, headSlug := readHead(f)
	tail, err := readTail(f)
	if err != nil {
		return info, err
	}

	for i := len(tail) - 1; i >= 0; i-- {
		var r record
		if json.Unmarshal(tail[i], &r) != nil {
			continue
		}
		if info.LastActivity.IsZero() {
			if t, err := time.Parse(time.RFC3339Nano, r.Timestamp); err == nil {
				info.LastActivity = t
			}
		}
		if info.Cwd == "" {
			info.Cwd = r.Cwd
		}
		if info.Branch == "" {
			info.Branch = r.GitBranch
		}
		if info.Slug == "" {
			info.Slug = r.Slug
		}
		if info.LastMessage == "" && r.Type == "assistant" {
			info.LastMessage = lastText(r)
		}
	}

	if headCwd != "" {
		info.Cwd = headCwd // the session's starting directory identifies it
	}
	if info.Branch == "" {
		info.Branch = headBranch
	}
	if info.Slug == "" {
		info.Slug = headSlug
	}
	if info.LastActivity.IsZero() {
		if st, err := f.Stat(); err == nil {
			info.LastActivity = st.ModTime()
		}
	}
	return info, nil
}

func readHead(f *os.File) (cwd, branch, slug string) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", "", ""
	}
	r := bufio.NewReaderSize(f, 64<<10)
	for i := 0; i < headLines; i++ {
		line, skipped, err := readBoundedLine(r, maxLine)
		var rec record
		if !skipped && json.Unmarshal(line, &rec) == nil {
			cwd = firstNonEmpty(cwd, rec.Cwd)
			branch = firstNonEmpty(branch, rec.GitBranch)
			slug = firstNonEmpty(slug, rec.Slug)
			if cwd != "" && branch != "" && slug != "" {
				return cwd, branch, slug
			}
		}
		if err != nil {
			break
		}
	}
	return cwd, branch, slug
}

// readBoundedLine reads one line, but never holds more than max bytes: a longer
// line is consumed whole and reported as skipped. A transcript line can be as
// large as a pasted file, and a crafted one must not exhaust memory.
func readBoundedLine(r *bufio.Reader, max int) (line []byte, skipped bool, err error) {
	var buf []byte
	for {
		chunk, rerr := r.ReadSlice('\n')
		if !skipped {
			if len(buf)+len(chunk) > max {
				skipped, buf = true, nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		switch {
		case rerr == nil:
			if skipped {
				return nil, true, nil
			}
			return buf, false, nil
		case errors.Is(rerr, bufio.ErrBufferFull):
			continue
		default:
			if skipped {
				return nil, true, rerr
			}
			return buf, false, rerr
		}
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// readTail returns the complete lines within the last tailBytes of the file.
func readTail(f *os.File) ([][]byte, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	off := max(st.Size()-tailBytes, 0)
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return nil, err
	}
	if off > 0 { // we landed mid-line: drop the partial first line
		if i := indexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		} else {
			return nil, nil
		}
	}
	var lines [][]byte
	for _, l := range strings.Split(string(buf), "\n") {
		if l != "" {
			lines = append(lines, []byte(l))
		}
	}
	return lines, nil
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// lastText returns the last text block of an assistant record, whitespace
// collapsed and truncated.
func lastText(r record) string {
	if r.Message == nil {
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(r.Message.Content, &blocks) != nil {
		return ""
	}
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].Type != "text" {
			continue
		}
		text := strings.Join(strings.Fields(blocks[i].Text), " ")
		if text == "" {
			continue
		}
		if runes := []rune(text); len(runes) > maxMessage {
			return string(runes[:maxMessage-1]) + "…"
		}
		return text
	}
	return ""
}
