//go:build !kartograph_test

package testhook

// At does nothing outside a test build.
func At(string) {}
