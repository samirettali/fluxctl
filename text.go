package main

import (
	"html"
	"regexp"
	"strings"
)

var (
	// selfClosing goes first: a lazy `<svg\b.*?</svg>` would otherwise start at `<svg .../>`
	// and swallow everything up to the next real `</svg>`.
	selfClosing = regexp.MustCompile(`(?is)<(script|style|noscript|svg|template)\b[^>]*/>`)
	dropBlocks  = regexp.MustCompile(`(?is)<script\b.*?</script\s*>|<style\b.*?</style\s*>|<noscript\b.*?</noscript\s*>|<svg\b.*?</svg\s*>|<template\b.*?</template\s*>`)
	paragraphs  = regexp.MustCompile(`(?i)</?(p|div|ul|ol|dl|h[1-6]|blockquote|pre|table|section|article|header|footer|figure|figcaption|hr)\b[^>]*>`)
	lineBreaks  = regexp.MustCompile(`(?i)<br\b[^>]*>|</(li|tr|dt|dd)\s*>`)
	cellBreaks  = regexp.MustCompile(`(?i)</(td|th)\s*>`)
	anyTag      = regexp.MustCompile(`(?s)<[^>]*>`)
	sourceSpace = regexp.MustCompile(`[\s\x{00a0}]+`)
	tabs        = regexp.MustCompile(` *\t *`)
	blankLines  = regexp.MustCompile(`\n{3,}`)
	lineSpacing = regexp.MustCompile(`(?m)^[ \t]+|[ \t]+$`)
)

// htmlToText flattens entry HTML into plain text: scripts and styles go, paragraphs become
// blank lines, list items and rows single line breaks, table cells a tab, every other tag is
// stripped, entities are decoded and whitespace is collapsed. It is a reading aid, not a
// parser: it only has to leave the prose readable. Line breaks and indentation inside
// <pre> are lost, since source whitespace is flattened before the tags are read.
func htmlToText(content string) string {
	text := selfClosing.ReplaceAllString(content, "")
	text = dropBlocks.ReplaceAllString(text, "")
	// Whitespace in HTML source carries no meaning, so flatten it before the tags become
	// the only line breaks and tabs in the text.
	text = sourceSpace.ReplaceAllString(text, " ")
	text = paragraphs.ReplaceAllString(text, "\n\n")
	text = lineBreaks.ReplaceAllString(text, "\n")
	text = cellBreaks.ReplaceAllString(text, "\t")
	text = anyTag.ReplaceAllString(text, "")
	text = strings.ReplaceAll(html.UnescapeString(text), "\u00a0", " ")
	text = tabs.ReplaceAllString(text, "\t")
	text = lineSpacing.ReplaceAllString(text, "")
	text = blankLines.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}
