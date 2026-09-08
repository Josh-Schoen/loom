// Copyright 2025 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"database/sql/driver"
	"errors"
	"io"
	"slices"
)

var knownSpecialErrors = [] error {
	driver.ErrBadConn,
	io.EOF,
}

var _ error = &specialError {} // implements error

type specialError struct {

	m_sMessage      string
	m_aerrSpecial   [] error

} // end type specialError

func (err *specialError) Error () string {

	return err.m_sMessage

} // end method Error

func (err *specialError) Is (target error) bool {

	for _, errSpecial := range err.m_aerrSpecial {
		if target == errSpecial {
			return true
		}
	}

	return false

} // end method Is

func preserveSpecialError (errs ... error) error {

	if len (errs) == 0 { return nil }

	aerrHave := [] error {}

	for _, errSpecial := range knownSpecialErrors {
		if errors.Is (errs [0], errSpecial) {
			aerrHave = append (aerrHave, errSpecial)
		}
	}

	aerrNeed := [] error {}

	for _, err := range errs [1 : ] {
		if err != nil {
			for _, errSpecial := range knownSpecialErrors {

				bHave        := slices.Contains (aerrHave, errSpecial)
				bAlreadyNeed := slices.Contains (aerrNeed, errSpecial)

				if ! bHave && ! bAlreadyNeed && errors.Is (err, errSpecial) {
					aerrNeed = append (aerrNeed, errSpecial)
				}
			} // end for errSpecial
		} // end if err
	} // end for err

	if len (aerrNeed) > 0 {
		return &specialError {
			m_sMessage    : errs [0].Error (),
			m_aerrSpecial : append (aerrHave, aerrNeed ...),
		}
	} else {
		return errs [0]
	}
} // end preserveSpecialError
