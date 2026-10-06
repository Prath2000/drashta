package claude

import (
	"bufio"
	"io"
	"os"
)

// MaxLine is the longest line the parser keeps (16 MB). A longer line is
// skipped and counted.
const MaxLine = 16 << 20

// lineReader streams complete lines. It never returns a last line that has no
// trailing "\n": that line may still be written, so it is left for next time.
type lineReader struct {
	r   *bufio.Reader
	off int64 // offset of the next unread line
	buf []byte
}

func newLineReader(r io.Reader, off int64) *lineReader {
	return &lineReader{r: bufio.NewReaderSize(r, 256<<10), off: off}
}

// openAt opens path and returns a reader for the bytes [off, limit).
// limit < 0 means read to the end of the file.
func openAt(path string, off, limit int64) (*os.File, *lineReader, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	if _, err := fh.Seek(off, io.SeekStart); err != nil {
		fh.Close()
		return nil, nil, err
	}
	var r io.Reader = fh
	if limit >= 0 {
		n := limit - off
		if n < 0 {
			n = 0
		}
		r = io.LimitReader(fh, n)
	}
	return fh, newLineReader(r, off), nil
}

// next returns the next complete line without "\n" (and without a "\r"), the
// offset where it starts and whether it was too long (then line is nil).
// The returned slice is valid until the next call. io.EOF means no more
// complete lines.
func (lr *lineReader) next() (line []byte, start int64, tooLong bool, err error) {
	lr.buf = lr.buf[:0]
	n := 0
	for {
		chunk, e := lr.r.ReadSlice('\n')
		n += len(chunk)
		switch e {
		case nil:
			start = lr.off
			lr.off += int64(n)
			if tooLong || len(lr.buf)+len(chunk)-1 > MaxLine {
				lr.buf = lr.buf[:0]
				return nil, start, true, nil
			}
			if len(lr.buf) > 0 {
				lr.buf = append(lr.buf, chunk...)
				chunk = lr.buf
			}
			chunk = chunk[:len(chunk)-1]
			if len(chunk) > 0 && chunk[len(chunk)-1] == '\r' {
				chunk = chunk[:len(chunk)-1]
			}
			return chunk, start, false, nil
		case bufio.ErrBufferFull:
			if !tooLong {
				if len(lr.buf)+len(chunk) > MaxLine {
					tooLong = true
					lr.buf = lr.buf[:0]
				} else {
					lr.buf = append(lr.buf, chunk...)
				}
			}
		default:
			// io.EOF (a partial tail is not consumed) or a read error.
			return nil, lr.off, false, e
		}
	}
}
