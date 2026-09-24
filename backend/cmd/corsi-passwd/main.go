// Command corsi-passwd turns a password into the argon2id verifier that
// AUTH_PASSWORD_HASH expects.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THIS RUNS ON THE OPERATOR'S MACHINE, NOT ON THE SERVER
//
// ══════════════════════════════════════════════════════════════════════
//
// The password is typed here and never leaves here. What travels to the
// deployment is the verifier, which is what the server needs and the only
// thing it can use.
//
// ── Why the password is never an argument ──────────────────────────────
// `corsi-passwd --password hunter2` would put the credential in argv, and
// argv is readable by every process on the machine through /proc and by
// anyone reading the shell history afterwards. There is no flag for it and
// there will not be one: the password is read from the terminal with echo
// off, or piped on stdin for a scripted run.
//
// Usage:
//
//	go run ./cmd/corsi-passwd              # prompts twice, no echo
//	printf '%s' "$PW" | go run ./cmd/corsi-passwd -stdin
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/corsi/backend/internal/platform/identity"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "corsi-passwd:", err)
		os.Exit(1)
	}
}

func run() error {
	fromStdin := flag.Bool("stdin", false, "read the password from stdin instead of prompting")
	flag.Parse()

	var password string
	var err error
	if *fromStdin {
		password, err = readPiped()
	} else {
		password, err = promptTwice()
	}
	if err != nil {
		return err
	}
	if len(password) < 12 {
		// Not a policy engine — one rule, and the one that matters against
		// an offline attack on a verifier that lives in an environment
		// variable. Length is the only property that reliably buys work.
		return errors.New("password must be at least 12 characters")
	}

	hash, err := identity.HashPassword(password)
	if err != nil {
		return err
	}

	// The verifier goes to stdout and nothing else does, so the output can
	// be piped or copied without capturing prose. The instructions go to
	// stderr.
	fmt.Fprintln(os.Stderr, "\nPaste this as AUTH_PASSWORD_HASH in the deployment's environment.")
	fmt.Fprintln(os.Stderr, "It is a verifier, not the password: the server cannot recover the password from it.")
	fmt.Println(hash)
	return nil
}

func readPiped() (string, error) {
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", err
		}
		return "", errors.New("no password on stdin")
	}
	return strings.TrimRight(sc.Text(), "\r\n"), nil
}

func promptTwice() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("stdin is not a terminal; use -stdin to pipe the password")
	}
	fmt.Fprint(os.Stderr, "password: ")
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "again: ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", errors.New("the two entries differ")
	}
	return string(first), nil
}
