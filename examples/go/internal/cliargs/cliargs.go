// Package cliargs lets the documented "<command> <input> [options]" form work.
//
// Go's flag package stops parsing at the first non-flag argument, so a command that documents the
// input first silently turns every following option into a positional argument and exits 2.
// Reorder rewrites the argv slice so the flag package sees options first and the input last.
package cliargs

import "strings"

// Reorder moves positional arguments to the end, keeping options and their values in order.
//
// takesValue must list every flag that consumes the following argument; the flag package does not
// expose that metadata, so each command passes its own set. Flags written as `-name=value` are
// recognised and left alone.
func Reorder(args []string, takesValue map[string]bool) []string {
	flags := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))

	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			positional = append(positional, args[index+1:]...)
			break
		}
		if len(arg) < 2 || arg[0] != '-' || arg == "-" {
			positional = append(positional, arg)
			continue
		}
		flags = append(flags, arg)
		name := strings.TrimLeft(arg, "-")
		if strings.ContainsRune(name, '=') || !takesValue[name] {
			continue
		}
		if index+1 < len(args) {
			index++
			flags = append(flags, args[index])
		}
	}
	return append(flags, positional...)
}
