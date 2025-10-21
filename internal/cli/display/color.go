// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package display

import (
	gkcolor "github.com/gookit/color"
)

func Gold(s string) string {
	return gkcolor.RGB(181, 181, 91).Sprint(s)
}
func Goldf(format string, args ...any) string {
	return gkcolor.RGB(181, 181, 91).Sprintf(format, args...)
}

func Green(s string) string {
	return gkcolor.FgGreen.Sprint(s)
}
func Greenf(format string, args ...any) string {
	return gkcolor.FgGreen.Sprintf(format, args...)
}

func Grey(s string) string {
	return gkcolor.RGB(138, 138, 138).Sprint(s)
}
func Greyf(format string, args ...any) string {
	return gkcolor.RGB(138, 138, 138).Sprintf(format, args...)
}

func LightBlue(s string) string {
	return gkcolor.HiBlue.Sprint(s)
}
func LightBluef(format string, args ...any) string {
	return gkcolor.HiBlue.Sprintf(format, args...)
}

func Red(s string) string {
	return gkcolor.FgRed.Sprint(s)
}
func Redf(s string, args ...any) string {
	return gkcolor.FgRed.Sprintf(s, args...)
}
