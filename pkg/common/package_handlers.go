package common

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

type PackageHandler interface {
	Install(string) error
	Remove(string) error
	List() ([]string, error)
}

func GetPackageHandler() (PackageHandler, error) {

	switch OsReleaseID() {
	case "arch":
		return &PackageHandler_Archlinux{}, nil
	case "freebsd":
		return &PackageHandler_FreeBSD{}, nil
	}

	return &PackageHandler_Null{}, fmt.Errorf("package handler not available for system")
}

type PackageHandler_Null struct{}

func (h *PackageHandler_Null) Install(_ string) error  { return nil }
func (h *PackageHandler_Null) Remove(_ string) error   { return nil }
func (h *PackageHandler_Null) List() ([]string, error) { return []string{}, nil }

// FREEBSD
type PackageHandler_FreeBSD struct{}

func (h *PackageHandler_FreeBSD) Install(name string) error {
	return simpleRunCommand("pkg", "install", "-qy", name)
}

func (h *PackageHandler_FreeBSD) Remove(name string) error {
	return simpleRunCommand("pkg", "remove", "-qy", name)
}
func (h *PackageHandler_FreeBSD) List() ([]string, error) {
	output, _, err := runCommand("pkg", "query", "-a", "'%n'")
	if err != nil {
		return []string{}, err
	}

	packages := strings.Split(output, "\n")

	return packages, nil
}

// ARCH
type PackageHandler_Archlinux struct{}

func (h *PackageHandler_Archlinux) Install(name string) error {
	return simpleRunCommand("/usr/bin/pacman", "-Sy", "--needed", name)
}

func (h *PackageHandler_Archlinux) Remove(name string) error {
	return simpleRunCommand("/usr/bin/pacman", "-Rcsy", name)
}
func (h *PackageHandler_Archlinux) List() ([]string, error) {
	output, _, err := runCommand("/usr/bin/pacman", "-Q")
	if err != nil {
		return []string{}, err
	}

	var packages []string

	reader := bytes.NewReader([]byte(output))
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) != 2 {
			return []string{}, fmt.Errorf("unknown output")
		}
		packages = append(packages, parts[0])
	}

	return packages, nil
}
