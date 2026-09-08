// Copyright 2025 by Teradata Corporation. All Rights Reserved.

//go:build wasm || !krb5 && !windows

package goteragss

func newKrb5Mech(loggerArg TdgssLogger, mi *mechanismInfo) gssp {

	return nil
}
