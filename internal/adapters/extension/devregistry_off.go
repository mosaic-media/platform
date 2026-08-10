// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

//go:build !mosaicdev

// The shipped half of the module-repository override (platform#55): there is no
// override. This file is what an ordinary `go build` compiles, and it is the
// whole of the mechanism in a release — two functions that read nothing and
// decide nothing.
//
// It is worth reading beside devregistry.go, which carries the reasoning. The
// short version: a variable that repoints the module repository can make the
// Platform download and run whatever it is pointed at, so the question is not
// "how loudly should a release warn about it" but "should a release be able to
// do it at all". This file is the answer. `official_test.go` asserts it by
// setting both variables and watching the compiled-in URL and key come back
// unchanged — the guard has a test in the build that ships, not only in the one
// that does not.
package extension

// devRepositoryOverride reports no override, without consulting the
// environment. Deliberately not "reads the environment and ignores it": there is
// no code path here from an environment value to a trusted key, which is a
// stronger statement than any branch could make.
func devRepositoryOverride() (devOverride, bool, error) {
	return devOverride{}, false, nil
}

// officialFetcher returns the guarded fetcher — the only one this build has.
// Module downloads go through netguard's dial guard like every other outbound
// fetch the Platform makes on a user's behalf (platform#50).
func officialFetcher() Fetcher {
	return NewHTTPFetcher()
}
