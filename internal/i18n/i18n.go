// Package i18n holds server-sent text (notifications, calendar feed names).
// Messages live in embedded <locale>.json files, never in code; the front end
// has its own files under web/src/lib/i18n.
package i18n

import (
	"embed"
	"encoding/json"
	"strings"
)

// Default is the fallback locale.
const Default = "en"

//go:embed *.json
var files embed.FS

var catalogs = load()

func load() map[string]map[string]string {
	entries, err := files.ReadDir(".")
	if err != nil {
		panic(err)
	}
	out := make(map[string]map[string]string, len(entries))
	for _, e := range entries {
		b, err := files.ReadFile(e.Name())
		if err != nil {
			panic(err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			panic("i18n: " + e.Name() + ": " + err.Error())
		}
		out[strings.TrimSuffix(e.Name(), ".json")] = m
	}
	if out[Default] == nil {
		panic("i18n: missing " + Default + ".json")
	}
	return out
}

// T returns the message key in locale ("en-GB" falls back to "en", unknown
// locales and missing keys to Default), with {name} placeholders replaced by
// params. A key missing everywhere is returned as is, so a gap is visible
// rather than silent. Parameter values are inserted verbatim (plain text).
func T(locale, key string, params map[string]string) string {
	msg, ok := lookup(locale, key)
	if !ok {
		return key
	}
	if len(params) == 0 {
		return msg
	}
	pairs := make([]string, 0, 2*len(params))
	for k, v := range params {
		pairs = append(pairs, "{"+k+"}", v)
	}
	// One pass: a value containing "{other}" is never expanded again.
	return strings.NewReplacer(pairs...).Replace(msg)
}

func lookup(locale, key string) (string, bool) {
	locale = strings.ToLower(strings.ReplaceAll(locale, "_", "-"))
	for _, l := range []string{locale, strings.SplitN(locale, "-", 2)[0], Default} {
		if m, ok := catalogs[l][key]; ok {
			return m, true
		}
	}
	return "", false
}
