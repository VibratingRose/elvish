package core

import (
	"errors"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/elves/elvish/edit/tty"
	"github.com/elves/elvish/edit/ui"
	"github.com/elves/elvish/styled"
	"github.com/elves/elvish/sys"
)

func TestReadCode_AbortsOnSetupError(t *testing.T) {
	terminal := newFakeTTY()
	terminal.setupErr = errors.New("a fake error")

	ed := NewEditor(terminal, nil)
	_, err := ed.ReadCode()

	if err != terminal.setupErr {
		t.Errorf("ReadCode returns error %v, want %v", err, terminal.setupErr)
	}
}

func TestReadCode_CallsRestore(t *testing.T) {
	restoreCalled := 0
	terminal := newFakeTTY()
	terminal.restoreFunc = func() { restoreCalled++ }
	terminal.eventCh <- tty.KeyEvent{Rune: '\n'}

	ed := NewEditor(terminal, nil)
	ed.ReadCode()

	if restoreCalled != 1 {
		t.Errorf("Restore callback called %d times, want once", restoreCalled)
	}
}

func TestReadCode_ResetsStateBeforeReturn(t *testing.T) {
	terminal := newFakeTTY()
	terminal.eventCh <- tty.KeyEvent{Rune: '\n'}

	ed := NewEditor(terminal, nil)
	ed.State.Code = "some code"
	ed.ReadCode()

	if ed.State.Code != "" {
		t.Errorf("Editor state has code %q, want empty", ed.State.Code)
	}
}

func TestReadCode_PassesInputEventsToMode(t *testing.T) {
	terminal := newFakeTTY()
	ed := NewEditor(terminal, nil)
	m := &fakeMode{maxKeys: 3}
	ed.State.Mode = m

	terminal.eventCh <- tty.KeyEvent{Rune: 'a'}
	terminal.eventCh <- tty.KeyEvent{Rune: 'b'}
	terminal.eventCh <- tty.KeyEvent{Rune: 'c'}

	ed.ReadCode()

	wantKeysHandled := []ui.Key{
		ui.Key{Rune: 'a'}, ui.Key{Rune: 'b'}, ui.Key{Rune: 'c'},
	}
	if !reflect.DeepEqual(m.keysHandled, wantKeysHandled) {
		t.Errorf("Mode gets keys %v, want %v", m.keysHandled, wantKeysHandled)
	}
}

func TestReadCode_CallsBeforeReadlineOnce(t *testing.T) {
	terminal := newFakeTTY()
	ed := NewEditor(terminal, nil)

	called := 0
	ed.Config.BeforeReadline = []func(){func() { called++ }}

	// Causes basicMode to quit
	terminal.eventCh <- tty.KeyEvent{Rune: '\n'}

	ed.ReadCode()

	if called != 1 {
		t.Errorf("BeforeReadline hook called %d times, want 1", called)
	}
}

func TestReadCode_CallsAfterReadlineOnceWithCode(t *testing.T) {
	terminal := newFakeTTY()
	ed := NewEditor(terminal, nil)

	called := 0
	code := ""
	ed.Config.AfterReadline = []func(string){func(s string) {
		called++
		code = s
	}}

	// Causes basicMode to write state.Code and then quit
	terminal.eventCh <- tty.KeyEvent{Rune: 'a'}
	terminal.eventCh <- tty.KeyEvent{Rune: 'b'}
	terminal.eventCh <- tty.KeyEvent{Rune: 'c'}
	terminal.eventCh <- tty.KeyEvent{Rune: '\n'}

	ed.ReadCode()

	if called != 1 {
		t.Errorf("AfterReadline hook called %d times, want 1", called)
	}
	if code != "abc" {
		t.Errorf("AfterReadline hook called with %q, want %q", code, "abc")
	}
}

func TestReadCode_RespectsMaxHeight(t *testing.T) {
	maxHeight := 5

	terminal := newFakeTTY()
	ed := NewEditor(terminal, nil)
	// Will fill more than maxHeight but less than terminal height
	ed.State.Code = strings.Repeat("a", 80*10)
	ed.State.Dot = len(ed.State.Code)

	codeCh, _ := ed.readCodeAsync()

	buf1 := <-terminal.bufCh
	// Make sure that normally the height does exceed maxHeight.
	if h := len(buf1.Lines); h <= maxHeight {
		t.Errorf("Buffer height is %d, should > %d", h, maxHeight)
	}

	ed.ConfigMutex.Lock()
	ed.Config.RenderConfig.MaxHeight = maxHeight
	ed.ConfigMutex.Unlock()

	ed.loop.Redraw(false)
	buf2 := <-terminal.bufCh
	if h := len(buf2.Lines); h > maxHeight {
		t.Errorf("Buffer height is %d, should <= %d", h, maxHeight)
	}

	terminal.eventCh <- tty.KeyEvent{Rune: '\n'}
	<-codeCh
}

var bufChTimeout = 1 * time.Second

func TestReadCode_RendersHighlightedCode(t *testing.T) {
	terminal := newFakeTTY()
	ed := NewEditor(terminal, nil)
	ed.Config.RenderConfig.Highlighter = func(code string) (styled.Text, []error) {
		return styled.Text{
			&styled.Segment{styled.Style{Foreground: "red"}, code}}, nil
	}

	terminal.eventCh <- tty.KeyEvent{Rune: 'a'}
	terminal.eventCh <- tty.KeyEvent{Rune: 'b'}
	terminal.eventCh <- tty.KeyEvent{Rune: 'c'}
	codeCh, _ := ed.readCodeAsync()

	wantBuf := ui.NewBufferBuilder(80).
		WriteString("abc", "31" /* SGR for red foreground */).
		SetDotToCursor().Buffer()
	if !terminal.checkBuffer(wantBuf) {
		t.Errorf("Did not see buffer containing highlighted code")
	}

	terminal.eventCh <- tty.KeyEvent{Rune: '\n'}
	<-codeCh
}

func TestReadCode_RendersErrorFromHighlighter(t *testing.T) {
	// TODO
}

func TestReadCode_RendersPrompt(t *testing.T) {
	// TODO
}

func TestReadCode_RendersRprompt(t *testing.T) {
	// TODO
}

func TestReadCode_SupportsPersistentRprompt(t *testing.T) {
	// TODO
}

func TestReadCode_UsesFinalStateInFinalRedraw(t *testing.T) {
	terminal := newFakeTTY()

	ed := NewEditor(terminal, nil)
	ed.State.Code = "some code"
	// We use the dot as a signal for distinguishing non-final and final state.
	// In the final state, the dot will be set to the length of the code (9).
	ed.State.Dot = 1

	codeCh, _ := ed.readCodeAsync()
	// Wait until a non-final state is drawn.
	wantBuf := ui.NewBufferBuilder(80).WriteUnstyled("s").SetDotToCursor().
		WriteUnstyled("ome code").Buffer()
	if !terminal.checkBuffer(wantBuf) {
		t.Errorf("did not get expected buffer before sending Enter")
	}

	terminal.eventCh <- tty.KeyEvent{Rune: '\n'}
	<-codeCh

	// Last element in bufs is nil
	finalBuf := terminal.bufs[len(terminal.bufs)-2]
	wantFinalBuf := ui.NewBufferBuilder(80).WriteUnstyled("some code").
		SetDotToCursor().Buffer()
	if !reflect.DeepEqual(finalBuf, wantFinalBuf) {
		t.Errorf("final buffer is %v, want %v", finalBuf, wantFinalBuf)
	}
}

func TestReadCode_QuitsOnSIGHUP(t *testing.T) {
	terminal := newFakeTTY()
	sigs := newFakeSignalSource()
	ed := NewEditor(terminal, sigs)

	codeCh, _ := ed.readCodeAsync()
	terminal.eventCh <- tty.KeyEvent{Rune: 'a'}
	wantBuf := ui.NewBufferBuilder(80).WriteUnstyled("a").
		SetDotToCursor().Buffer()
	if !terminal.checkBuffer(wantBuf) {
		t.Errorf("did not get expected buffer before sending SIGHUP")
	}

	sigs.ch <- syscall.SIGHUP

	select {
	case <-codeCh:
		// TODO: Test that ReadCode returns with io.EOF
	case <-time.After(time.Second):
		t.Errorf("SIGHUP did not cause ReadCode to return")
	}
}

func TestReadCode_ResetsOnSIGHUP(t *testing.T) {
	terminal := newFakeTTY()
	sigs := newFakeSignalSource()
	ed := NewEditor(terminal, sigs)

	codeCh, _ := ed.readCodeAsync()
	terminal.eventCh <- tty.KeyEvent{Rune: 'a'}
	wantBuf := ui.NewBufferBuilder(80).WriteUnstyled("a").
		SetDotToCursor().Buffer()
	if !terminal.checkBuffer(wantBuf) {
		t.Errorf("did not get expected buffer before sending SIGINT")
	}

	sigs.ch <- syscall.SIGINT

	wantBuf = ui.NewBufferBuilder(80).Buffer()
	if !terminal.checkBuffer(wantBuf) {
		t.Errorf("Terminal state is not reset after SIGINT")
	}

	terminal.eventCh <- tty.KeyEvent{Rune: '\n'}
	<-codeCh
}

func TestReadCode_RedrawsOnSIGWINCH(t *testing.T) {
	terminal := newFakeTTY()
	sigs := newFakeSignalSource()
	ed := NewEditor(terminal, sigs)

	ed.State.Code = "1234567890"
	ed.State.Dot = len(ed.State.Code)

	codeCh, _ := ed.readCodeAsync()
	wantBuf := ui.NewBufferBuilder(80).WriteUnstyled("1234567890").
		SetDotToCursor().Buffer()
	if !terminal.checkBuffer(wantBuf) {
		t.Errorf("did not get expected buffer before sending SIGWINCH")
	}

	terminal.setSize(24, 4)
	sigs.ch <- sys.SIGWINCH

	wantBuf = ui.NewBufferBuilder(4).WriteUnstyled("1234567890").
		SetDotToCursor().Buffer()
	if !terminal.checkBuffer(wantBuf) {
		t.Errorf("Terminal is not redrawn after SIGWINCH")
	}

	terminal.eventCh <- tty.KeyEvent{Rune: '\n'}
	<-codeCh
}
