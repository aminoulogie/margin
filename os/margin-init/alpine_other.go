//go:build !linux

package main

func alpineMounts() error { return nil }

func setHostname() {}

func runInAlpine(args ...string) error { return nil }

func outputInAlpine(args ...string) ([]byte, error) { return nil, nil }

func outputInAlpineDirect(args ...string) ([]byte, error) { return nil, nil }
