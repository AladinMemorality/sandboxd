//go:build !linux

package main

import "context"

func run(context.Context, string) error { return failed }
