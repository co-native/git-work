package cli

import "io"

// Sections writes per-repo output blocks and separates consecutive blocks that
// printed something with one blank line. Multi-repo commands run the same logic
// once per repo, so without a separator their output reads as one wall of text;
// a skipped repo contributes no block and therefore no separator.
//
// Call Begin at the start of each repo's block, then write that repo's output
// through the Sections value:
//
//	s := cli.NewSections(w)
//	for _, r := range scope {
//		s.Begin()
//		report(s, r)
//	}
//
// The first block never gets a leading blank line. Sections implements
// io.Writer, so it composes with fmt.Fprintf and repo.PrintCommits.
type Sections struct {
	w       io.Writer
	pending bool // Begin called and this block has not written yet
	printed bool // an earlier block wrote at least one byte
}

// NewSections returns a Sections that writes blocks to w.
func NewSections(w io.Writer) *Sections {
	return &Sections{w: w}
}

// Begin starts a new repo's block. Any output written before the next call to
// Begin belongs to it; a block that writes nothing is dropped along with its
// separator.
func (s *Sections) Begin() {
	s.pending = true
}

// Write implements io.Writer and emits the separator lazily, before the first
// byte of a block that follows one that printed. A block that prints nothing
// therefore adds no stray blank line.
func (s *Sections) Write(p []byte) (int, error) {
	if len(p) > 0 && s.pending {
		s.pending = false
		if s.printed {
			if _, err := io.WriteString(s.w, "\n"); err != nil {
				return 0, err
			}
		}
		s.printed = true
	}
	return s.w.Write(p)
}
