//go:build !windows

package system

import "errors"

// ErrNoDialog is returned where no native picker exists.
var ErrNoDialog = errors.New("no native file dialog on this platform")

func (s *Service) PickFolder(title string) (string, error) { return "", ErrNoDialog }

func (s *Service) PickFile(title string) (string, error) { return "", ErrNoDialog }
