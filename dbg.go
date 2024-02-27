package runn

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/elk-language/go-prompt"
	pstrings "github.com/elk-language/go-prompt/strings"
	"github.com/k0kubun/pp/v3"
	"github.com/olekukonko/tablewriter"
)

const (
	dbgCmdNext          = "next"
	dbgCmdNextShort     = "n"
	dbgCmdQuit          = "quit"
	dbgCmdQuitShort     = "q"
	dbgCmdPrint         = "print"
	dbgCmdPrintShort    = "p"
	dbgCmdBreak         = "break"
	dbgCmdBreakShort    = "b"
	dbgCmdContinue      = "continue"
	dbgCmdContinueShort = "c"
	dbgCmdInfo          = "info"
	dbgCmdInfoShort     = "i"
	dbgCmdList          = "list"
	dbgCmdListShort     = "l"
)

const bpSep = ":"

type breakpoint struct {
	runbookID string
	stepKey   string
}

// dbg is runn debugger.
type dbg struct {
	enable      bool
	showPrompt  bool
	quit        bool
	history     []string
	breakpoints []breakpoint
	ops         *operators
	pp          *pp.PrettyPrinter
}

func newDBG(enable bool) *dbg {
	return &dbg{
		enable:     enable,
		showPrompt: true,
		pp:         pp.New(),
	}
}

type completer struct {
	dbg  *dbg
	step *step
}

func newCompleter(dbg *dbg, step *step) *completer {
	return &completer{
		dbg:  dbg,
		step: step,
	}
}

func (c *completer) do(d prompt.Document) ([]prompt.Suggest, pstrings.RuneNumber, pstrings.RuneNumber) {
	endIndex := d.CurrentRuneIndex()
	w := d.GetWordBeforeCursor()
	startIndex := endIndex - pstrings.RuneCount([]byte(w))

	cmd := d.Text
	splitted := strings.Split(cmd, " ")
	var s []prompt.Suggest
	switch {
	case !strings.Contains(cmd, " "):
		s = []prompt.Suggest{
			{Text: dbgCmdNext, Description: "run current step and next"},
			{Text: dbgCmdQuit, Description: "quit debugger and skip all steps"},
			{Text: dbgCmdContinue, Description: "continue to run until next breakpoint"},
			{Text: dbgCmdPrint, Description: "print variable"},
			{Text: dbgCmdBreak, Description: "set breakpoint"},
			{Text: dbgCmdInfo, Description: "show information"},
			{Text: dbgCmdList, Description: "list codes of step"},
		}
	case splitted[0] == dbgCmdPrint || splitted[0] == dbgCmdPrintShort:
		store := c.step.parent.store.toMap()
		store[storeRootKeyIncluded] = c.step.parent.included
		store[storeRootKeyPrevious] = c.step.parent.store.latest()
		keys := storeKeys(store)
		for _, k := range keys {
			if strings.HasPrefix(k, w) {
				s = append(s, prompt.Suggest{Text: k})
			}
		}
	}

	return prompt.FilterHasPrefix(s, w, true), startIndex, endIndex
}

func (d *dbg) attach(ctx context.Context, s *step) error {
	prpt := "> "

	if d.quit {
		s.parent.skipped = true
		return errStepSkiped
	}
	if !d.enable {
		return nil
	}

	if s != nil {
		id := s.parent.ID()
		stepKey := s.key
		stepIdx := strconv.Itoa(s.idx)
		// check breakpoints
		for _, bp := range d.breakpoints {
			if !strings.HasPrefix(id, bp.runbookID) {
				continue
			}
			if bp.stepKey != stepKey && bp.stepKey != stepIdx {
				continue
			}
			d.showPrompt = true
		}
		prpt = fmt.Sprintf("%s[%s]> ", id[:7], s.key)
	}

	if !d.showPrompt {
		return nil
	}
	d.showPrompt = false

L:
	for {
		in := prompt.Input(
			prompt.WithPrefix(prpt),
			prompt.WithCompleter(newCompleter(d, s).do),
			prompt.WithHistory(d.history),
		)
		d.history = append(d.history, in)
		cmd := strings.SplitN(strings.TrimSpace(in), " ", 2)
		prog := cmd[0]
		switch prog {
		case dbgCmdNext, dbgCmdNextShort:
			// next
			d.showPrompt = true
			break L
		case dbgCmdContinue, dbgCmdContinueShort:
			// continue
			break L
		case dbgCmdQuit, dbgCmdQuitShort:
			// quit
			d.quit = true
			s.parent.skipped = true
			return errStepSkiped
		case dbgCmdPrint, dbgCmdPrintShort:
			// print
			if len(cmd) != 2 {
				_, _ = fmt.Fprintf(os.Stderr, "args required")
				continue
			}
			store := s.parent.store.toMap()
			store[storeRootKeyIncluded] = s.parent.included
			store[storeRootKeyPrevious] = s.parent.store.latest()
			e, err := Eval(cmd[1], store)
			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "%s\n", err.Error())
				continue
			}
			d.pp.Println(e)
		case dbgCmdBreak, dbgCmdBreakShort:
			// break
			if len(cmd) != 2 {
				_, _ = fmt.Fprintf(os.Stderr, "args required")
				continue
			}
			splitted := strings.Split(cmd[1], bpSep)
			bp := breakpoint{}
			if splitted[0] != "" {
				bp.runbookID = splitted[0]
			} else {
				bp.runbookID = s.parent.ID()
			}
			if len(splitted) > 1 && splitted[1] != "" {
				bp.stepKey = splitted[1]
			} else {
				bp.stepKey = "0"
			}
			d.breakpoints = append(d.breakpoints, bp)
		case dbgCmdInfo, dbgCmdInfoShort:
			// info
			if len(cmd) != 2 {
				_, _ = fmt.Fprintf(os.Stderr, "args required")
				continue
			}
			switch cmd[1] {
			case "breakpoints", "b":
				table := tablewriter.NewWriter(os.Stdout)
				table.SetHeader([]string{"Num", "ID", "Step"})
				table.SetAutoWrapText(false)
				table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
				table.SetAutoFormatHeaders(false)
				table.SetCenterSeparator("")
				table.SetColumnSeparator("")
				table.SetRowSeparator("-")
				table.SetHeaderLine(false)
				table.SetBorder(false)
				for i, bp := range d.breakpoints {
					table.Append([]string{strconv.Itoa(i + 1), bp.runbookID, bp.stepKey})
				}
				table.Render()
			default:
				_, _ = fmt.Fprintf(os.Stderr, "unknown args %s\n", cmd[1])
				continue
			}
		case dbgCmdList, dbgCmdListShort:
			// list
			var (
				path string
				idx  int
			)
			if len(cmd) != 2 {
				if s == nil {
					_, _ = fmt.Fprintf(os.Stderr, "invalid args %s\n", cmd[1])
					continue
				}
				path = s.parent.bookPath
				idx = s.idx
			} else {
				splitted := strings.Split(cmd[1], bpSep)
				var (
					id string
					o  *operator
				)
				if splitted[0] != "" {
					id = splitted[0]
				} else {
					id = s.parent.ID()
				}

				// search runbook
				found := false
				for _, op := range d.ops.ops {
					if strings.HasPrefix(op.ID(), id) {
						if found {
							_, _ = fmt.Fprintf(os.Stderr, "unable to identify runbook: %s\n", id)
							continue L
						}
						o = op
						found = true
					}
				}
				if !found {
					_, _ = fmt.Fprintf(os.Stderr, "runbook not found: %s\n", id)
					continue L
				}
				path = o.bookPath

				if len(splitted) > 1 && splitted[1] != "" {
					i, err := strconv.Atoi(splitted[1])
					if err != nil {
						found := false
						for _, s := range o.steps {
							if s.key == splitted[1] {
								found = true
								idx = s.idx
							}
						}
						if !found {
							_, _ = fmt.Fprintf(os.Stderr, "step not found: %s\n", splitted[1])
							continue L
						}
					} else {
						idx = i
					}
				} else {
					idx = 0
				}

			}
			b, err := readFile(path)
			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
				continue
			}
			picked, err := pickStepYAML(string(b), idx)
			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
				continue
			}
			fmt.Println(picked)
		default:
			_, _ = fmt.Fprintf(os.Stderr, "unknown command %s\n", in)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return nil
}

// storeKeys
func storeKeys(store map[string]any) []string {
	const storeKeySep = "."
	var keys []string
	for k := range store {
		keys = append(keys, k)
		switch v := store[k].(type) {
		case map[string]any:
			subKeys := storeKeys(v)
			for _, sk := range subKeys {
				keys = append(keys, k+storeKeySep+sk)
			}
		}
	}
	sort.Strings(keys)
	return keys
}
