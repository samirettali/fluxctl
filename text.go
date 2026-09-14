package main

import (
	"html"
	"regexp"
	"strings"
)

var (
	dropBlocks  = regexp.MustCompile(`(?is)<script\b.*?</script\s*>|<style\b.*?</style\s*>|<noscript\b.*?</noscript\s*>|<svg\b.*?</svg\s*>|<template\b.*?</template\s*>`)
	blockBreaks = regexp.MustCompile(`(?i)</?(p|div|br|li|ul|ol|h[1-6]|blockquote|pre|tr|table|section|article|header|footer|figure|figcaption|hr)\b[^>]*>`)
	anyTag      = regexp.MustCompile(`(?s)<[^>]*>`)
	spaces      = regexp.MustCompile(`[ \t\r\f\v\x{00a0}]+`)
	blankLines  = regexp.MustCompile(`\n{3,}`)
	lineSpacing = regexp.MustCompile(`(?m)^[ \t]+|[ \t]+$`)
)

// htmlToText flattens entry HTML into plain text: scripts and styles go, block elements
// become line breaks, every other tag is stripped, entities are decoded and whitespace is
// collapsed. It is a reading aid, not a parser: it only has to leave the prose readable.
func htmlToText(content string) string {
	text := dropBlocks.ReplaceAllString(content, "")
	text = blockBreaks.ReplaceAllString(text, "\n")
	text = anyTag.ReplaceAllString(text, "")
	text = html.UnescapeString(text)
	text = spaces.ReplaceAllString(text, " ")
	text = lineSpacing.ReplaceAllString(text, "")
	text = blankLines.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}
