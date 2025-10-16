package util

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/term"
)

func GetState() (*term.State, error) {
	state, err := term.GetState(int(os.Stdin.Fd()))
	if err != nil {
		return nil, err
	}
	return state, nil
}

func NewRaw() (*term.State, error) {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, err
	}
	return oldState, nil
}

func ResetRaw(oldState *term.State) error {
	if oldState == nil {
		fmt.Print("\033[2J\033[H")
		time.Sleep(300 * time.Millisecond)
		return nil
	}
	err := term.Restore(int(os.Stdin.Fd()), oldState)
	fmt.Print("\033[2J\033[H")
	time.Sleep(300 * time.Millisecond)
	return err
}
