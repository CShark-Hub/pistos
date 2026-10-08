package main

import (
	"errors"
	"strings"
)

var errEmpty = errors.New("empty name")

func normalize(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errEmpty
	}
	return strings.ToLower(name), nil
}

func sum(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}
