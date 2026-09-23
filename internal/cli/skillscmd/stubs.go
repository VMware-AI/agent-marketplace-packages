package skillscmd

// errNotImplemented is a tiny helper so the stub bodies stay one-liners.
// It deliberately uses a non-typed error so cobra's exit-code-on-error
// path returns 1 (not a custom code).
func errNotImplemented(cmd, phase string) error {
	return errUnimplemented{cmd: cmd, phase: phase}
}

type errUnimplemented struct {
	cmd, phase string
}

func (e errUnimplemented) Error() string {
	return e.cmd + ": not implemented yet (planned for " + e.phase + ")"
}