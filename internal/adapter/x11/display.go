// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package x11

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

func displayAddress(display string) (*displayTarget, error) {
	before, after, ok := strings.CutLast(display, displaySeparator)
	if !ok {
		return nil, fmt.Errorf(detailErrorFormat, errInvalidDisplay, display)
	}

	target, err := displayNumbers(after)
	if err != nil {
		return nil, fmt.Errorf("parse display numbers: %w", err)
	}

	setDisplayAddress(target, before, beforeScreen(after))

	return target, nil
}

func displayNumbers(suffix string) (*displayTarget, error) {
	number, err := nonnegativeNumber(beforeScreen(suffix), errInvalidDisplay)
	if err != nil {
		return nil, fmt.Errorf("parse display number: %w", err)
	}

	screen, err := displayScreen(suffix)
	if err != nil {
		return nil, fmt.Errorf("parse screen number: %w", err)
	}

	return &displayTarget{
		network: noValue,
		address: noValue,
		host:    noValue,
		number:  number,
		screen:  screen,
	}, nil
}

func displayScreen(suffix string) (int, error) {
	separator := strings.IndexByte(suffix, screenByte)
	if separator < zero || separator == len(suffix)-oneWord {
		return zero, nil
	}

	number, err := nonnegativeNumber(suffix[separator+oneWord:], errInvalidScreen)
	if err != nil {
		return zero, fmt.Errorf("parse display screen: %w", err)
	}

	return number, nil
}

func nonnegativeNumber(value string, invalid error) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil || number < zero {
		return zero, fmt.Errorf(detailErrorFormat, invalid, value)
	}

	return number, nil
}

func setDisplayAddress(target *displayTarget, prefix, digits string) {
	if strings.HasPrefix(prefix, pathSeparator) {
		target.network, target.address = localHost, prefix+displaySeparator+digits

		return
	}

	setDisplayHost(target, prefix)

	if target.host == noValue || target.host == localHost {
		target.host, target.network, target.address = noValue, localHost, unixSocketDir+socketPrefix+digits

		return
	}

	setRemoteAddress(target)
}

func setDisplayHost(target *displayTarget, prefix string) {
	target.network, target.host = localHost, prefix

	protocol, hostname, found := strings.Cut(prefix, pathSeparator)

	if found {
		target.network, target.host = protocol, hostname
	}
}

func setRemoteAddress(target *displayTarget) {
	if target.network == localHost {
		target.network = remoteNetwork
	}

	target.address = net.JoinHostPort(target.host, strconv.Itoa(x11TCPPort+target.number))
}
