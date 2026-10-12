// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package x11

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// authorityCookie matches the connection's host and display number.
func authorityCookie(host string, number int) (string, error) {
	cookie, err := authorityCookieForHost(authorityHost(host, os.Hostname), number)
	if err != nil {
		return noValue, fmt.Errorf("resolve authority cookie: %w", err)
	}

	return cookie, nil
}

func authorityCookieForHost(host string, number int) (string, error) {
	if host == noValue {
		return noValue, nil
	}

	data := optionalAuthorityFile(authorityPath())
	if data == nil {
		return noValue, nil
	}

	cookie, err := findAuthority(bytes.NewReader(data), host, strconv.Itoa(number))
	if err != nil {
		return noValue, fmt.Errorf("select authority: %w", err)
	}

	return cookie, nil
}

func authorityField(reader io.Reader) ([]byte, error) {
	var length uint16

	err := binary.Read(reader, binary.BigEndian, &length)
	if err != nil {
		return nil, fmt.Errorf("read authority length: %w", err)
	}

	value, err := readAuthorityValue(reader, length)
	if err != nil {
		return nil, fmt.Errorf("decode authority value: %w", err)
	}

	return value, nil
}

func readAuthorityValue(reader io.Reader, length uint16) ([]byte, error) {
	value, err := io.ReadAll(io.LimitReader(reader, int64(length)))
	if err != nil {
		return nil, fmt.Errorf(authorityValueErrorFormat, err)
	}

	if len(value) != int(length) {
		return nil, fmt.Errorf(authorityValueErrorFormat, io.ErrUnexpectedEOF)
	}

	return value, nil
}

func authorityHost(host string, hostname func() (string, error)) string {
	if host != noValue && host != localhost {
		return host
	}

	resolved, err := hostname()
	if err != nil {
		return noValue
	}

	return resolved
}

func authorityPath() string {
	path := os.Getenv("XAUTHORITY")
	if path != noValue {
		return path
	}

	home := os.Getenv("HOME")
	if home == noValue {
		return noValue
	}

	return filepath.Join(home, authorityFilename)
}

func findAuthority(reader io.Reader, host, display string) (string, error) {
	record := matchingAuthority(reader, host, display)
	if record == nil {
		return noValue, nil
	}

	cookie, err := recordCookie(record)
	if err != nil {
		return noValue, fmt.Errorf("decode authority cookie: %w", err)
	}

	return cookie, nil
}

func matchingAuthority(reader io.Reader, host, display string) *authorityRecord {
	for {
		record := optionalAuthorityRecord(reader)
		if record == nil {
			return nil
		}

		if authorityMatches(record, host, display) {
			return record
		}
	}
}

func readAuthorityFile(path string) ([]byte, error) {
	file, err := openAuthorityFile(path)
	if err != nil {
		return nil, fmt.Errorf("open authority: %w", err)
	}

	data, readErr := io.ReadAll(file)

	err = errors.Join(readErr, file.Close())
	if err != nil {
		return nil, fmt.Errorf("read authority: %w", err)
	}

	return data, nil
}

// Resolve authority-file links before confining the open to the target directory.
func openAuthorityFile(path string) (*os.File, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve authority path: %w", err)
	}

	file, err := os.OpenInRoot(filepath.Dir(resolved), filepath.Base(resolved))
	if err != nil {
		return nil, fmt.Errorf("open resolved authority: %w", err)
	}

	return file, nil
}

func readAuthorityRecord(reader io.Reader) (*authorityRecord, error) {
	var record authorityRecord

	err := binary.Read(reader, binary.BigEndian, &record.family)
	if err != nil {
		return nil, fmt.Errorf("read authority family: %w", err)
	}

	err = readAuthorityFields(reader, &record.fields)
	if err != nil {
		return nil, fmt.Errorf("read authority record: %w", err)
	}

	return &record, nil
}

func recordCookie(record *authorityRecord) (string, error) {
	if string(record.fields[2]) != cookieProtocol || len(record.fields[3]) != cookieBytes {
		return noValue, fmt.Errorf(detailErrorFormat, errAuthorityProtocol, record.fields[2])
	}

	return hex.EncodeToString(record.fields[3]), nil
}

func authorityMatches(record *authorityRecord, host, display string) bool {
	local := record.family == authorityLocal && string(record.fields[0]) == host
	number := string(record.fields[1])

	return (record.family == authorityWildcard || local) && (number == noValue || number == display)
}

func readAuthorityFields(reader io.Reader, fields *[wordBytes][]byte) error {
	for i := range fields {
		value, err := authorityField(reader)
		if err != nil {
			return fmt.Errorf("read authority field: %w", err)
		}

		fields[i] = value
	}

	return nil
}

func optionalAuthorityFile(path string) []byte {
	data, err := readAuthorityFile(path)
	if err != nil {
		return nil
	}

	return data
}

func optionalAuthorityRecord(reader io.Reader) *authorityRecord {
	record, err := readAuthorityRecord(reader)
	if err != nil {
		return nil
	}

	return record
}
