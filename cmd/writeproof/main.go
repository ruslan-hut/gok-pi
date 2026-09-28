// Command writeproof proves the Modbus write path against a Huawei SmartLogger
// by writing a register's current value back to it, once with function code
// 0x06 and once with 0x10. The words written are the words just read, so the
// device ends up exactly as it was; what the run shows is that both function
// codes are accepted and that this host has write permission.
//
// It is stage 3 of the bring-up in doc/huawei-integration.md. Only registers on
// a short allowlist can be named, always at unit 0, and only while their
// current value lies inside the documented range: a value outside it (as 42470
// reads on the Pedernoso site) would not be written back unchanged in any
// meaningful sense. Without -yes it reads, checks and prints the plan, and
// writes nothing.
//
// Usage:
//
//	writeproof -addr 10.0.80.91:502         # dry run
//	writeproof -addr 10.0.80.91:502 -yes
//
// essprobe never writes; this is the only command that links modbus.Writer.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"gok-pi/battery/driver/huawei"
	"gok-pi/internal/modbus"
)

// loggerUnit is the SmartLogger's own unit id, where every allowlisted
// register lives.
const loggerUnit = 0

// allowed are the registers writeproof may write back. Each is an ordinary RW
// parameter at unit 0 that is not a dispatch input.
var allowed = map[uint16]huawei.Register{
	huawei.LoggerCommTimeout.Addr: huawei.LoggerCommTimeout,
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "writeproof:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("writeproof", flag.ContinueOnError)
	fs.SetOutput(out)
	var (
		addr    = fs.String("addr", "", "SmartLogger endpoint as host:port")
		regAddr = fs.Uint("reg", uint(huawei.LoggerCommTimeout.Addr), "register to write back; allowed: "+allowedList())
		timeout = fs.Duration("timeout", 5*time.Second, "per-request timeout")
		yes     = fs.Bool("yes", false, "perform the writes; without it the run is a dry run")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *addr == "" {
		return errors.New("-addr is required")
	}

	reg, ok := allowed[uint16(*regAddr)]
	if !ok || *regAddr > 0xFFFF {
		return fmt.Errorf("register %d is not on the allowlist (%s)", *regAddr, allowedList())
	}

	c := modbus.New(*addr, *timeout)
	defer func() { _ = c.Close() }()

	words, err := c.ReadHoldingRegisters(ctx, loggerUnit, reg.Addr, reg.Words())
	if err != nil {
		return err
	}
	if err := checkWritable(reg, words); err != nil {
		return err
	}
	fmt.Fprintf(out, "read %s = %s (words %04X)\n", reg, describe(reg, words), words)

	if !*yes {
		fmt.Fprintf(out, "dry run: would write %04X back to %d with 0x06 and 0x10; rerun with -yes\n", words, reg.Addr)

		return nil
	}

	w := modbus.NewWriter(c)
	steps := []struct {
		name  string
		write func() error
	}{
		{"0x06", func() error { return w.WriteSingleRegister(ctx, loggerUnit, reg.Addr, words[0]) }},
		{"0x10", func() error { return w.WriteMultipleRegisters(ctx, loggerUnit, reg.Addr, words) }},
	}
	if len(words) > 1 {
		// 0x06 writes one register; a wider value can only go through 0x10.
		steps = steps[1:]
		fmt.Fprintf(out, "0x06 skipped: %s spans %d registers\n", reg, len(words))
	}

	for _, st := range steps {
		if err := st.write(); err != nil {
			return fmt.Errorf("%s: %w%s", st.name, err, hint(err))
		}

		back, err := c.ReadHoldingRegisters(ctx, loggerUnit, reg.Addr, reg.Words())
		if err != nil {
			return fmt.Errorf("%s accepted, but reading it back failed: %w", st.name, err)
		}
		if !slices.Equal(back, words) {
			return fmt.Errorf("%s accepted, but %d now reads %04X instead of %04X; restore %s",
				st.name, reg.Addr, back, words, describe(reg, words))
		}
		fmt.Fprintf(out, "%s accepted, read back %s: unchanged\n", st.name, describe(reg, back))
	}

	fmt.Fprintln(out, "write path proven: value unchanged")

	return nil
}

// checkWritable refuses a register whose current value could not be written
// back as a no-op: never set, or outside its documented range.
func checkWritable(reg huawei.Register, words []uint16) error {
	raw, err := reg.DecodeRaw(words)
	if err != nil {
		return err
	}
	if reg.Unset(raw) {
		return fmt.Errorf("%s reads the \"not set\" value; nothing to write back", reg)
	}

	v, err := reg.Decode(words)
	if err != nil {
		return err
	}
	if reg.Bounded && (v < reg.Min || v > reg.Max) {
		return fmt.Errorf("%s reads %g, outside its documented range [%g, %g]; refusing to write it back", reg, v, reg.Min, reg.Max)
	}

	return nil
}

func describe(reg huawei.Register, words []uint16) string {
	v, err := reg.Decode(words)
	if err != nil {
		return fmt.Sprintf("%04X", words)
	}
	if reg.Unit == "" {
		return fmt.Sprintf("%g", v)
	}

	return fmt.Sprintf("%g %s", v, reg.Unit)
}

// hint explains the exceptions a write is most likely to meet on this site.
func hint(err error) string {
	var exc modbus.Exception
	if !errors.As(err, &exc) {
		return ""
	}

	switch exc {
	case modbus.ExceptionNoPermission:
		return " (is this host on the logger's Modbus TCP whitelist?)"
	case modbus.ExceptionInvalidFunction:
		return " (the logger does not accept this function code here)"
	}

	return ""
}

func allowedList() string {
	addrs := make([]int, 0, len(allowed))
	for a := range allowed {
		addrs = append(addrs, int(a))
	}
	slices.Sort(addrs)

	return fmt.Sprint(addrs)
}
