package main

import "fmt"

// red colors the given string to red, with ANSI/VT100 88/256
// color sequences
func red(s string) string {
	return fmt.Sprintf("\x1b[1;31m%s\x1b[0m", s)
}

// yellow colors the given string to yellow, with ANSI/VT100 88/256
// color sequences
func yellow(s string) string {
	return fmt.Sprintf("\x1b[1;33m%s\x1b[0m", s)
}

// blue colors the given string to blue, with ANSI/VT100 88/256
// color sequences
func blue(s string) string {
	return fmt.Sprintf("\x1b[1;34m%s\x1b[0m", s)
}

// magenta colors the given string to magenta, with ANSI/VT100 88/256
// color sequences
func magenta(s string) string {
	return fmt.Sprintf("\x1b[1;35m%s\x1b[0m", s)
}

// green colors the given string to green, with ANSI/VT100 88/256
// color sequences
func green(s string) string {
	return fmt.Sprintf("\x1b[1;32m%s\x1b[0m", s)
}
