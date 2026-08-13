// Package drive owns the concrete Google Docs API adapter. This file converts
// the canonical script document HTML into Docs API structural requests.
//
// Google Drive's HTML import (Files.Create + text/html media) silently drops
// <h1> titles and <pre><code> blocks and flattens <h2> headings to normal
// text, which is exactly the surface the document renderer relies on for the
// caller-facing title and the SpecScene JSON snapshot. Building the document
// structurally via the Docs API preserves both.
package drive

import (
	"fmt"
	"strings"
	"unicode/utf16"

	"golang.org/x/net/html"
	"google.golang.org/api/docs/v1"
)

// docBlockStyle identifies the structural role of a parsed document block.
type docBlockStyle int

const (
	blockTitle docBlockStyle = iota
	blockHeading
	blockNormal
	blockCode
)

// docRun is one inline text run with optional bold and/or link styling.
type docRun struct {
	text string
	bold bool
	link string
}

// docBlock is one structural block (title / heading / paragraph / code line)
// parsed from the canonical document HTML.
type docBlock struct {
	style docBlockStyle
	runs  []docRun
}

func (b docBlock) text() string {
	var sb strings.Builder
	for _, r := range b.runs {
		sb.WriteString(r.text)
	}
	return sb.String()
}

func (b docBlock) namedStyleType() string {
	switch b.style {
	case blockTitle:
		return "HEADING_1"
	case blockHeading:
		return "HEADING_2"
	default:
		return "NORMAL_TEXT"
	}
}

// BuildInsertRequests parses canonical document HTML and returns the Docs API
// requests that insert it structurally, preserving the title heading and the
// SpecScene JSON code block that Drive's HTML import would otherwise drop.
func BuildInsertRequests(content string) ([]*docs.Request, error) {
	root, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("parse document html: %w", err)
	}
	var blocks []docBlock
	collectDocumentBlocks(root, &blocks)
	return buildDocumentInsertRequests(blocks), nil
}

func collectDocumentBlocks(n *html.Node, blocks *[]docBlock) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch c.Data {
		case "head", "title", "meta", "link", "style", "script":
			// Metadata nodes carry no visible document content.
		case "h1":
			*blocks = append(*blocks, docBlock{style: blockTitle, runs: collectInlineRuns(c)})
		case "h2":
			*blocks = append(*blocks, docBlock{style: blockHeading, runs: collectInlineRuns(c)})
		case "p":
			*blocks = append(*blocks, docBlock{style: blockNormal, runs: collectInlineRuns(c)})
		case "pre":
			for _, line := range strings.Split(collectText(c), "\n") {
				if line == "" {
					continue
				}
				*blocks = append(*blocks, docBlock{style: blockCode, runs: []docRun{{text: line}}})
			}
		default:
			// Container element (html, body, section, div, ...): recurse.
			collectDocumentBlocks(c, blocks)
		}
	}
}

// collectInlineRuns flattens inline content into runs, marking <strong>/<b>
// as bold and <a href> as links.
func collectInlineRuns(n *html.Node) []docRun {
	var runs []docRun
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, bold bool) {
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			switch c.Type {
			case html.TextNode:
				if c.Data != "" {
					runs = append(runs, docRun{text: c.Data, bold: bold})
				}
			case html.ElementNode:
				switch c.Data {
				case "strong", "b":
					walk(c, true)
				case "a":
					collectLinkRuns(c, &runs, bold)
				default:
					walk(c, bold)
				}
			}
		}
	}
	walk(n, false)
	return runs
}

func collectLinkRuns(n *html.Node, runs *[]docRun, bold bool) {
	href := nodeAttr(n, "href")
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			if c.Data != "" {
				*runs = append(*runs, docRun{text: c.Data, bold: bold, link: href})
			}
		case html.ElementNode:
			collectLinkRuns(c, runs, bold)
		}
	}
}

func collectText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			switch c.Type {
			case html.TextNode:
				sb.WriteString(c.Data)
			case html.ElementNode:
				walk(c)
			}
		}
	}
	walk(n)
	return sb.String()
}

func nodeAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// utf16Len returns the number of UTF-16 code units in s, matching the unit the
// Docs API uses for Range indexes. Go's builtin len reports bytes, which drifts
// for any non-ASCII text (accented letters, emoji).
func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// buildDocumentInsertRequests converts parsed blocks into Docs API requests.
// Blocks are joined with paragraph breaks and inserted at the document start
// (index 1); heading styles, inline bold/link runs, and code monospace are
// applied over the corresponding text ranges. All Range indexes are measured in
// UTF-16 code units, matching the Docs API.
func buildDocumentInsertRequests(blocks []docBlock) []*docs.Request {
	// Drop empty blocks up front so the joined text and the per-block offset
	// walk stay in lockstep (an empty block would otherwise add a stray "\n"
	// that the offset loop never accounts for).
	var nonEmpty []docBlock
	for _, b := range blocks {
		if b.text() != "" {
			nonEmpty = append(nonEmpty, b)
		}
	}
	if len(nonEmpty) == 0 {
		return nil
	}

	var parts []string
	for _, b := range nonEmpty {
		parts = append(parts, b.text())
	}
	joined := strings.Join(parts, "\n")

	reqs := []*docs.Request{
		{
			InsertText: &docs.InsertTextRequest{
				Location: &docs.Location{Index: 1},
				Text:     joined,
			},
		},
	}

	offset := int64(1)
	for _, b := range nonEmpty {
		text := b.text()
		end := offset + int64(utf16Len(text))

		reqs = append(reqs, &docs.Request{
			UpdateParagraphStyle: &docs.UpdateParagraphStyleRequest{
				Range:          &docs.Range{StartIndex: offset, EndIndex: end},
				ParagraphStyle: &docs.ParagraphStyle{NamedStyleType: b.namedStyleType()},
				Fields:         "namedStyleType",
			},
		})

		if b.style == blockCode {
			reqs = append(reqs, &docs.Request{
				UpdateTextStyle: &docs.UpdateTextStyleRequest{
					Range:     &docs.Range{StartIndex: offset, EndIndex: end},
					TextStyle: &docs.TextStyle{WeightedFontFamily: &docs.WeightedFontFamily{FontFamily: "Courier New"}},
					Fields:    "weightedFontFamily.fontFamily",
				},
			})
		} else {
			runOffset := int64(0)
			for _, run := range b.runs {
				if run.text == "" {
					continue
				}
				if !run.bold && run.link == "" {
					runOffset += int64(utf16Len(run.text))
					continue
				}
				var fields []string
				style := &docs.TextStyle{}
				if run.bold {
					style.Bold = true
					fields = append(fields, "bold")
				}
				if run.link != "" {
					style.Link = &docs.Link{Url: run.link}
					fields = append(fields, "link")
				}
				reqs = append(reqs, &docs.Request{
					UpdateTextStyle: &docs.UpdateTextStyleRequest{
						Range:     &docs.Range{StartIndex: offset + runOffset, EndIndex: offset + runOffset + int64(utf16Len(run.text))},
						TextStyle: style,
						Fields:    strings.Join(fields, ","),
					},
				})
				runOffset += int64(utf16Len(run.text))
			}
		}
		offset = end + 1
	}
	return reqs
}
