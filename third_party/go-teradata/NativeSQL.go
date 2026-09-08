// Copyright 2017-2022 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type blockElement struct {

	iBlockStart       int
	bFunc             bool
	bCall             bool
	nParenDepth       int
	nParenPairCount   int
	aiSeparators      [] int

} // end type blockElement

type blockStack struct {

	a [] blockElement

} // end type blockStack

func (stack *blockStack) pushBlock (e blockElement) {

	stack.a = append (stack.a, e)

} // end method pushBlock

func (stack *blockStack) popBlock () (e blockElement) {

	iLast    := len (stack.a) - 1
	e         = stack.a [iLast]
	stack.a   = stack.a [ : iLast]
	return

} // end method popBlock

func (stack *blockStack) peekBlock () (pe *blockElement) {

	iLast   := len (stack.a) - 1
	pe       = &(stack.a [iLast])
	return

} // end method peekBlock

const ( Outside = iota ; DashDashComment ; SlashStarComment ; SingleQuoteString ; DoubleQuoteString ; PassThru )

var reD      = regexp.MustCompile ("(?i)^\\{d\\s+")
var reT      = regexp.MustCompile ("(?i)^\\{t\\s+")
var reTS     = regexp.MustCompile ("(?i)^\\{ts\\s+")
var reFN     = regexp.MustCompile ("(?i)^\\{fn\\s+")
var reOJ     = regexp.MustCompile ("(?i)^\\{oj\\s+")
var reCALL   = regexp.MustCompile ("(?i)^\\{call\\s+")
var reESCAPE = regexp.MustCompile ("(?i)^\\{escape\\s+")

func toNativeSQL (rows *TeradataRows) (err error) {

	sInput := rows.m_stmt.m_sRequestText
	bPartitionFastExport := strings.EqualFold (rows.m_stmt.m_con.m_params.M_sPartition, "EXPORT")

	// If the SQL request text has a trailing semicolon followed by whitespace, then the database considers it
	// to be a multi-statement request, which is incompatible with batch insert. We trim whitespace to avoid:
	// [Teradata Database] [Error 6908] Iterated request: Multi-statement requests not supported.
	if ! bPartitionFastExport {
		sInput = strings.TrimSpace (sInput) // GOSQL-75
	}

	var abyOutput      [] byte
	var stack          blockStack

	for nState := Outside ; len (sInput) > 0 ; {

		if bPartitionFastExport && rows.m_bSendOnly && len (sInput) == rows.m_stmt.m_con.m_nFastExportReqMsgLen {
			abyOutput = append (abyOutput, sInput [ : rows.m_stmt.m_con.m_nFastExportReqMsgLen] ...)
			break
		}

		var nByteCount int

		switch {
			case processToken (Outside          , [] string {"\r", "\n"        }, Outside          , &nState, sInput, &abyOutput, &nByteCount) :
			case processToken (Outside          , [] string {"'"               }, SingleQuoteString, &nState, sInput, &abyOutput, &nByteCount) :
			case processToken (SingleQuoteString, [] string {"'"               }, Outside          , &nState, sInput, &abyOutput, &nByteCount) :
			case processToken (Outside          , [] string {"\""              }, DoubleQuoteString, &nState, sInput, &abyOutput, &nByteCount) :
			case processToken (DoubleQuoteString, [] string {"\""              }, Outside          , &nState, sInput, &abyOutput, &nByteCount) :
			case processToken (Outside          , [] string {"--"              }, DashDashComment  , &nState, sInput, &abyOutput, &nByteCount) :
			case processToken (DashDashComment  , [] string {"\r", "\n"        }, Outside          , &nState, sInput, &abyOutput, &nByteCount) :
			case processToken (Outside          , [] string {"/*"              }, SlashStarComment , &nState, sInput, &abyOutput, &nByteCount) :
			case processToken (SlashStarComment , [] string {"*/"              }, Outside          , &nState, sInput, &abyOutput, &nByteCount) :
			case processToken (Outside          , [] string {"@BEGIN_PASS_THRU"}, PassThru         , &nState, sInput, &abyOutput, &nByteCount) :
			case processToken (PassThru         , [] string {"@END_PASS_THRU"  }, Outside          , &nState, sInput, &abyOutput, &nByteCount) :

			case nState == Outside && sInput [0] == '{':

				block := blockElement { iBlockStart : len (abyOutput) }

				switch {
					case processEscapeClause (reD     , "date "     , sInput, &abyOutput, &nByteCount) :
					case processEscapeClause (reT     , "time "     , sInput, &abyOutput, &nByteCount) :
					case processEscapeClause (reTS    , "timestamp ", sInput, &abyOutput, &nByteCount) :
					case processEscapeClause (reFN    , ""          , sInput, &abyOutput, &nByteCount) : block.bFunc = true
					case processEscapeClause (reOJ    , ""          , sInput, &abyOutput, &nByteCount) :
					case processEscapeClause (reCALL  , "call "     , sInput, &abyOutput, &nByteCount) : block.bCall = true
					case processEscapeClause (reESCAPE, "escape "   , sInput, &abyOutput, &nByteCount) :

					default:

						err = rows.m_stmt.m_con.makeDriverError (nil, 0, fmt.Sprintf ("Unrecognized escape syntax %v", sInput))
						return

				} // end switch

				stack.pushBlock (block)

			case nState == Outside && sInput [0] == '}':

				nByteCount = 1

				if len (stack.a) == 0 {
					err = rows.m_stmt.m_con.makeDriverError (nil, 0, "Unexpected escape syntax character }")
					return
				}

				block := stack.popBlock ()

				if block.nParenDepth > 0 {
					err = rows.m_stmt.m_con.makeDriverError (nil, 0, "Too many open parentheses inside escape syntax")
					return
				}

				if block.bCall && block.nParenPairCount == 0 { // if CALL contains no parentheses

					for abyOutput [len (abyOutput) - 1] == ';' { // trim trailing semicolons if present
						abyOutput = abyOutput [ : len (abyOutput) - 1]
					}

					abyOutput = append (abyOutput, "()" ...) // append empty parentheses

				} else if block.bFunc {

					err = processEscapeBlock (rows, block, &abyOutput)
					if err != nil { return }
				}

			default:

				if nState == Outside && len (stack.a) > 0 { // if examining an escape block

					pblock := stack.peekBlock ()

					switch sInput [0] {

						case '(':

							if pblock.nParenDepth == 0 {

								if pblock.nParenPairCount > 0 {
									err = rows.m_stmt.m_con.makeDriverError (nil, 0, "Too many pairs of parentheses inside escape syntax")
									return
								}

								pblock.aiSeparators = append (pblock.aiSeparators, len (abyOutput))
							}

							pblock.nParenDepth++

						case ',':

							if pblock.nParenDepth == 1 {
								pblock.aiSeparators = append (pblock.aiSeparators, len (abyOutput))
							}

						case ')':

							pblock.nParenDepth--

							if pblock.nParenDepth == 0 {

								pblock.nParenPairCount++
								pblock.aiSeparators = append (pblock.aiSeparators, len (abyOutput))

							} else if pblock.nParenDepth < 0 {

								err = rows.m_stmt.m_con.makeDriverError (nil, 0, "Too many close parentheses inside escape syntax")
								return
							}
					} // end switch
				} // end if

				_, nByteCount = utf8.DecodeRuneInString (sInput)
				abyOutput = append (abyOutput, sInput [ : nByteCount] ...)

		} // end switch

		sInput = sInput [nByteCount : ]

	} // end for

	if len (stack.a) != 0 {
		err = rows.m_stmt.m_con.makeDriverError (nil, 0, "Missing escape syntax character }")
		return
	}

	rows.m_sNativeSQL = string (abyOutput)

	// Post processing of rows.m_sNativeSQL occurs here, for logic such as validating mutually-exclusive options.

	if rows.m_byAGKR != 0 {
		if rows.m_stmt.m_con.m_config.M_uAGKRSupport != 1 {
			err = rows.m_stmt.m_con.makeDriverError (nil, 0, "Database does not support Auto-Generated Key Retrieval (AGKR)")
			return
		}

		if rows.m_bTryFastLoad || rows.m_bRequireFastLoad {
			err = rows.m_stmt.m_con.makeDriverError (nil, 0, "Auto-Generated Key Retrieval (AGKR) cannot be used with FastLoad")
			return
		}

		if rows.m_bTryFastExport || rows.m_bRequireFastExport {
			err = rows.m_stmt.m_con.makeDriverError (nil, 0, "Auto-Generated Key Retrieval (AGKR) cannot be used with FastExport")
			return
		}
	}

	if rows.m_sFieldQuote == rows.m_sFieldSeparator {

		err = rows.m_stmt.m_con.makeDriverError (nil, 550,
			fmt.Sprintf ("Different values must be specified for the field quote and field separator characters, but the same value %v was used",
				rows.m_sFieldQuote))

		return
	}

	if rows.m_sExportPathName != "" {

		if rows.m_bNoExecute { // clear values if we are not executing the request
			rows.m_sExportPathName = ""
			rows.m_nFileExporterType = 0

		} else {
			err = ensureWritableFile (rows.m_sExportPathName) // reuse existing file, or create file if it does not exist
			if err != nil {
				err = rows.m_stmt.m_con.makeDriverError (err, 548,
					fmt.Sprintf ("Escape function %v specifies an inaccessible file %v",
						ternary (rows.m_nFileExporterType == CSV_FILE_TYPE, "TERADATA_WRITE_CSV", "unknown"),
						rows.m_sExportPathName))

				return
			}
		}
	} // end if m_sExportPathName

	if rows.m_bLSN {

		if ! rows.m_bNoExecute {
			err = rows.m_stmt.m_con.makeDriverError (nil, 0, "Escape function teradata_nativesql is required with escape function teradata_logon_sequence_number")
			return
		}

		if rows.m_bGetErrors {
			err = rows.m_stmt.m_con.makeDriverError (nil, 0, "Escape functions teradata_logon_sequence_number and teradata_get_errors cannot be used together")
			return
		}

		if rows.m_bGetWarnings {
			err = rows.m_stmt.m_con.makeDriverError (nil, 0, "Escape functions teradata_logon_sequence_number and teradata_get_warnings cannot be used together")
			return
		}

		if rows.m_sNativeSQL == "" {
			rows.m_sNativeSQL = format10Uint32 (rows.m_stmt.m_con.m_uLogonSequenceNumber)
		} else if mgr, bPresent := rows.m_stmt.m_con.m_mapRequestToManager [strings.TrimSpace (rows.m_sNativeSQL)] ; bPresent {
			switch typedval := mgr.(type) {
				case fastLoadManager:
					fldManager := typedval.Value ()
					if fldManager != nil && fldManager.isConnected () {
						rows.m_sNativeSQL = format10Uint32 (fldManager.getControlSession ().m_uLogonSequenceNumber)
					} else {
						rows.m_sNativeSQL = ""
					}
				case fastExportManager:
					fxpManager := typedval.Value ()
					if fxpManager != nil && fxpManager.isConnected () {
						rows.m_sNativeSQL = format10Uint32 (fxpManager.getControlSession ().m_uLogonSequenceNumber)
					} else {
						rows.m_sNativeSQL = ""
					}
				default:
					rows.m_sNativeSQL = ""
			}
		} else {
			rows.m_sNativeSQL = ""
		}
	} // end if rows.m_bLSN

	if rows.m_bGetErrors {

		if ! rows.m_bNoExecute {
			err = rows.m_stmt.m_con.makeDriverError (nil, 0, "Escape function teradata_nativesql is required with escape function teradata_get_errors")
			return
		}

		if rows.m_bGetWarnings {
			err = rows.m_stmt.m_con.makeDriverError (nil, 0, "Escape functions teradata_get_errors and teradata_get_warnings cannot be used together")
			return
		}

		rows.m_sNativeSQL = strings.Join (rows.m_stmt.m_con.m_mapRequestToErrors [rows.m_sNativeSQL], "\n") // strings.Join on nil slice returns empty string ""

	} // end if rows.m_bGetErrors

	if rows.m_bGetWarnings {

		if ! rows.m_bNoExecute {
			err = rows.m_stmt.m_con.makeDriverError (nil, 0, "Escape function teradata_nativesql is required with escape function teradata_get_warnings")
			return
		}

		if mgr, bPresent := rows.m_stmt.m_con.m_mapRequestToManager [strings.TrimSpace (rows.m_sNativeSQL)] ; bPresent {

			switch typedval := mgr.(type) {

				case fastLoadManager:

					fldManager := typedval.Value ()
					rows.m_sNativeSQL = strings.Join (fldManager.getWarnings (), "\n")

				case fastExportManager:

					fxpManager := typedval.Value ()
					rows.m_sNativeSQL = strings.Join (fxpManager.getWarnings (), "\n")

				default:
					rows.m_sNativeSQL = ""

			} // end switch
		} else {
				rows.m_sNativeSQL = ""
		}
	} // end if rows.m_bGetWarnings

	return

} // end func toNativeSQL

func processToken (nInputState int, asMatch [] string, nOutputState int, pnState *int, sInput string, pabyOutput *[] byte, pnByteCount *int) (bOutput bool) {

	if *pnState != nInputState { return }

	for i := 0 ; ! bOutput && i < len (asMatch) ; i++ {
		bOutput = strings.HasPrefix (sInput, asMatch [i])
	}

	if bOutput {
		*pabyOutput  = append (*pabyOutput, asMatch [0] ...) // append the first item regardless of which item matched, to replace newlines with CRs
		*pnByteCount = len (asMatch [0])
		*pnState     = nOutputState
	}

	return

} // end func processToken

func processEscapeClause (re *regexp.Regexp, sReplace string, sInput string, pabyOutput *[] byte, pnByteCount *int) (bOutput bool) {

	ai := re.FindStringSubmatchIndex (sInput)
	if ai == nil { return }

	*pabyOutput  = append (*pabyOutput, sReplace ...)
	*pnByteCount = ai [1]
	bOutput      = true
	return

} // end func processEscapeClause

var reATAN2         = regexp.MustCompile ("(?i)^ATAN2\\s*"        )
var reCHAR          = regexp.MustCompile ("(?i)^CHAR\\s*"         )
var reCONCAT        = regexp.MustCompile ("(?i)^CONCAT\\s*"       )
var reCONVERT       = regexp.MustCompile ("(?i)^CONVERT\\s*"      )
var reCOT           = regexp.MustCompile ("(?i)^COT\\s*"          )
var reCURDATE       = regexp.MustCompile ("(?i)^CURDATE\\s*"      )
var reCURTIME       = regexp.MustCompile ("(?i)^CURTIME\\s*"      )
var reDAYOFMONTH    = regexp.MustCompile ("(?i)^DAYOFMONTH\\s*"   )
var reDIFFERENCE    = regexp.MustCompile ("(?i)^DIFFERENCE\\s*"   )
var reHOUR          = regexp.MustCompile ("(?i)^HOUR\\s*"         )
var reIFNULL        = regexp.MustCompile ("(?i)^IFNULL\\s*"       )
var reINSERT        = regexp.MustCompile ("(?i)^INSERT\\s*"       )
var reLCASE         = regexp.MustCompile ("(?i)^LCASE\\s*"        )
var reLEFT          = regexp.MustCompile ("(?i)^LEFT\\s*"         )
var reLENGTH        = regexp.MustCompile ("(?i)^LENGTH\\s*"       )
var reLOCATE        = regexp.MustCompile ("(?i)^LOCATE\\s*"       )
var reLOG           = regexp.MustCompile ("(?i)^LOG\\s*"          )
var reLOG10         = regexp.MustCompile ("(?i)^LOG10\\s*"        )
var reLTRIM         = regexp.MustCompile ("(?i)^LTRIM\\s*"        )
var reMINUTE        = regexp.MustCompile ("(?i)^MINUTE\\s*"       )
var reMOD           = regexp.MustCompile ("(?i)^MOD\\s*"          )
var reMONTH         = regexp.MustCompile ("(?i)^MONTH\\s*"        )
var reNOW           = regexp.MustCompile ("(?i)^NOW\\s*"          )
var rePI            = regexp.MustCompile ("(?i)^PI\\s*"           )
var reRAND          = regexp.MustCompile ("(?i)^RAND\\s*"         )
var reREPEAT        = regexp.MustCompile ("(?i)^REPEAT\\s*"       )
var reREPLACE       = regexp.MustCompile ("(?i)^REPLACE\\s*"      )
var reRIGHT         = regexp.MustCompile ("(?i)^RIGHT\\s*"        )
var reRTRIM         = regexp.MustCompile ("(?i)^RTRIM\\s*"        )
var reSECOND        = regexp.MustCompile ("(?i)^SECOND\\s*"       )
var reSPACE         = regexp.MustCompile ("(?i)^SPACE\\s*"        )
var reSUBSTRING     = regexp.MustCompile ("(?i)^SUBSTRING\\s*"    )
var reTIMESTAMPADD  = regexp.MustCompile ("(?i)^TIMESTAMPADD\\s*" )
var reTIMESTAMPDIFF = regexp.MustCompile ("(?i)^TIMESTAMPDIFF\\s*")
var reTRUNCATE      = regexp.MustCompile ("(?i)^TRUNCATE\\s*"     )
var reUCASE         = regexp.MustCompile ("(?i)^UCASE\\s*"        )
var reWEEK          = regexp.MustCompile ("(?i)^WEEK\\s*"         )
var reYEAR          = regexp.MustCompile ("(?i)^YEAR\\s*"         )

// When shorter names match the beginning of longer names, the longer names must be tested first.

var reTERADATA_AGKR                         = regexp.MustCompile ("(?i)^TERADATA_AGKR\\s*"                            )
var reTERADATA_AMP_COUNT                    = regexp.MustCompile ("(?i)^TERADATA_AMP_COUNT\\s*"                       )
var reTERADATA_ARRAY_TRANSFORM_OFF          = regexp.MustCompile ("(?i)^TERADATA_ARRAY_TRANSFORM_OFF\\s*"             )
var reTERADATA_ARRAY_TRANSFORM_ON           = regexp.MustCompile ("(?i)^TERADATA_ARRAY_TRANSFORM_ON\\s*"             )
var reTERADATA_AUTOCOMMIT_OFF               = regexp.MustCompile ("(?i)^TERADATA_AUTOCOMMIT_OFF\\s*"                  )
var reTERADATA_AUTOCOMMIT_ON                = regexp.MustCompile ("(?i)^TERADATA_AUTOCOMMIT_ON\\s*"                   )
var reTERADATA_AUTOCOMMIT                   = regexp.MustCompile ("(?i)^TERADATA_AUTOCOMMIT\\s*"                      ) // must be checked last
var reTERADATA_BYPASS_TX_COMMANDS           = regexp.MustCompile ("(?i)^TERADATA_BYPASS_TX_COMMANDS\\s*"              )
var reTERADATA_CLOBTRANSLATE                = regexp.MustCompile ("(?i)^TERADATA_CLOBTRANSLATE\\s*"                   )
var reTERADATA_COMMIT                       = regexp.MustCompile ("(?i)^TERADATA_COMMIT\\s*"                          )
var reTERADATA_CONNECTED                    = regexp.MustCompile ("(?i)^TERADATA_CONNECTED\\s*"                       )
var reTERADATA_DATABASE_VERSION             = regexp.MustCompile ("(?i)^TERADATA_DATABASE_VERSION\\s*"                )
var reTERADATA_DRIVER_VERSION               = regexp.MustCompile ("(?i)^TERADATA_DRIVER_VERSION\\s*"                  )
var reTERADATA_ERROR_QUERY_COUNT            = regexp.MustCompile ("(?i)^TERADATA_ERROR_QUERY_COUNT\\s*"               )
var reTERADATA_ERROR_QUERY_INTERVAL         = regexp.MustCompile ("(?i)^TERADATA_ERROR_QUERY_INTERVAL\\s*"            )
var reTERADATA_ERROR_TABLE_1_SUFFIX         = regexp.MustCompile ("(?i)^TERADATA_ERROR_TABLE_1_SUFFIX\\s*"            )
var reTERADATA_ERROR_TABLE_2_SUFFIX         = regexp.MustCompile ("(?i)^TERADATA_ERROR_TABLE_2_SUFFIX\\s*"            )
var reTERADATA_ERROR_TABLE_DATABASE         = regexp.MustCompile ("(?i)^TERADATA_ERROR_TABLE_DATABASE\\s*"            )
var reTERADATA_FAILFAST                     = regexp.MustCompile ("(?i)^TERADATA_FAILFAST\\s*"                        )
var reTERADATA_FAKE_RESULT_SETS_OFF         = regexp.MustCompile ("(?i)^TERADATA_FAKE_RESULT_SETS_OFF\\s*"            ) // must be checked first
var reTERADATA_FAKE_RESULT_SETS             = regexp.MustCompile ("(?i)^TERADATA_FAKE_RESULT_SETS\\s*"                )
var reTERADATA_FIELD_QUOTE                  = regexp.MustCompile ("(?i)^TERADATA_FIELD_QUOTE\\s*"                     )
var reTERADATA_FIELD_SEP                    = regexp.MustCompile ("(?i)^TERADATA_FIELD_SEP\\s*"                       )
var reTERADATA_GET_ERRORS                   = regexp.MustCompile ("(?i)^TERADATA_GET_ERRORS\\s*"                      )
var reTERADATA_GET_WARNINGS                 = regexp.MustCompile ("(?i)^TERADATA_GET_WARNINGS\\s*"                    )
var reTERADATA_GETLOGLEVEL                  = regexp.MustCompile ("(?i)^TERADATA_GETLOGLEVEL\\s*"                     )
var reTERADATA_GO_DISTRIBUTION              = regexp.MustCompile ("(?i)^TERADATA_GO_DISTRIBUTION\\s*"                 )
var reTERADATA_GO_RUNTIME                   = regexp.MustCompile ("(?i)^TERADATA_GO_RUNTIME\\s*"                      )
var reTERADATA_GOVERN_OFF                   = regexp.MustCompile ("(?i)^TERADATA_GOVERN_OFF\\s*"                      )
var reTERADATA_GOVERN_ON                    = regexp.MustCompile ("(?i)^TERADATA_GOVERN_ON\\s*"                       )
var reTERADATA_LOBSELECT                    = regexp.MustCompile ("(?i)^TERADATA_LOBSELECT\\s*"                       )
var reTERADATA_LOGON_SEQUENCE_NUMBER        = regexp.MustCompile ("(?i)^TERADATA_LOGON_SEQUENCE_NUMBER\\s*"           )
var reTERADATA_MANAGE_ERROR_TABLES_OFF      = regexp.MustCompile ("(?i)^TERADATA_MANAGE_ERROR_TABLES_OFF\\s*"         )
var reTERADATA_MANAGE_ERROR_TABLES_ON       = regexp.MustCompile ("(?i)^TERADATA_MANAGE_ERROR_TABLES_ON\\s*"          )
var reTERADATA_NATIVESQL                    = regexp.MustCompile ("(?i)^TERADATA_NATIVESQL\\s*"                       )
var reTERADATA_PARAMETER                    = regexp.MustCompile ("(?i)^TERADATA_PARAMETER\\s*"                       )
var reTERADATA_POSIXLT_OFF                  = regexp.MustCompile ("(?i)^TERADATA_POSIXLT_OFF\\s*"                     )
var reTERADATA_POSIXLT_ON                   = regexp.MustCompile ("(?i)^TERADATA_POSIXLT_ON\\s*"                      )
var reTERADATA_PROGRAM_NAME                 = regexp.MustCompile ("(?i)^TERADATA_PROGRAM_NAME\\s*"                    )
var reTERADATA_PROVIDE                      = regexp.MustCompile ("(?i)^TERADATA_PROVIDE\\s*"                         )
var reTERADATA_READ_CSV                     = regexp.MustCompile ("(?i)^TERADATA_READ_CSV\\s*"                        )
var reTERADATA_READ_PARQUET                 = regexp.MustCompile ("(?i)^TERADATA_READ_PARQUET\\s*"                    )
var reTERADATA_REQUEST_TIMEOUT              = regexp.MustCompile ("(?i)^TERADATA_REQUEST_TIMEOUT\\s*"                 )
var reTERADATA_REQUIRE_FASTEXPORT           = regexp.MustCompile ("(?i)^TERADATA_REQUIRE_FASTEXPORT\\s*"              )
var reTERADATA_REQUIRE_FASTLOAD             = regexp.MustCompile ("(?i)^TERADATA_REQUIRE_FASTLOAD\\s*"                )
var reTERADATA_ROLLBACK                     = regexp.MustCompile ("(?i)^TERADATA_ROLLBACK\\s*"                        )
var reTERADATA_ROWS_HANDLE                  = regexp.MustCompile ("(?i)^TERADATA_ROWS_HANDLE\\s*"                     )
var reTERADATA_RPO                          = regexp.MustCompile ("(?i)^TERADATA_RPO\\s*"                             )
var reTERADATA_SEND_RUN_STARTUP_PARCEL      = regexp.MustCompile ("(?i)^TERADATA_SEND_RUN_STARTUP_PARCEL\\s*"         )
var reTERADATA_SESSION_NUMBER               = regexp.MustCompile ("(?i)^TERADATA_SESSION_NUMBER\\s*"                  )
var reTERADATA_SESSIONS                     = regexp.MustCompile ("(?i)^TERADATA_SESSIONS\\s*"                        )
var reTERADATA_SET_FILE_EXPORTER            = regexp.MustCompile ("(?i)^TERADATA_SET_FILE_EXPORTER\\s*"               )
var reTERADATA_SOCKET_INFO                  = regexp.MustCompile ("(?i)^TERADATA_SOCKET_INFO\\s*"                     )
var reTERADATA_TRY_FASTEXPORT               = regexp.MustCompile ("(?i)^TERADATA_TRY_FASTEXPORT\\s*"                  )
var reTERADATA_TRY_FASTLOAD                 = regexp.MustCompile ("(?i)^TERADATA_TRY_FASTLOAD\\s*"                    )
var reTERADATA_UDT_TRANSFORMS_OFF           = regexp.MustCompile ("(?i)^TERADATA_UDT_TRANSFORMS_OFF\\s*"              )
var reTERADATA_UDT_TRANSFORMS_ON            = regexp.MustCompile ("(?i)^TERADATA_UDT_TRANSFORMS_ON\\s*"               )
var reTERADATA_UNTRUSTED                    = regexp.MustCompile ("(?i)^TERADATA_UNTRUSTED\\s*"                       )
var reTERADATA_VALUES_OFF                   = regexp.MustCompile ("(?i)^TERADATA_VALUES_OFF\\s*"                      )
var reTERADATA_VALUES_ON                    = regexp.MustCompile ("(?i)^TERADATA_VALUES_ON\\s*"                       )
var reTERADATA_WEB_SERVICE                  = regexp.MustCompile ("(?i)^TERADATA_WEB_SERVICE\\s*"                     )
var reTERADATA_WRITE_CSV                    = regexp.MustCompile ("(?i)^TERADATA_WRITE_CSV\\s*"                       )

func processEscapeBlock (rows *TeradataRows, block blockElement, pabyOutput *[] byte) (err error) {

	// Input string syntax checking guarantees the first sep is open paren, the last sep is close paren, and all intermediate seps are commas.

	abyFuncName := (*pabyOutput) [block.iBlockStart : ]
	nActualArgCount := 0
	sAllArgs := ""

	if len (block.aiSeparators) > 0 {
		abyFuncName = (*pabyOutput) [block.iBlockStart : block.aiSeparators [0]]
		nActualArgCount = len (block.aiSeparators) - 1
		sAllArgs = strings.TrimSpace (string ((*pabyOutput) [block.aiSeparators [0] + 1 : block.aiSeparators [nActualArgCount]]))
	}

	a1 := ""
	a2 := ""
	a3 := ""
	a4 := ""

	if nActualArgCount >= 1 { a1 = strings.TrimSpace (string ((*pabyOutput) [block.aiSeparators [0] + 1 : block.aiSeparators [1]])) }
	if nActualArgCount >= 2 { a2 = strings.TrimSpace (string ((*pabyOutput) [block.aiSeparators [1] + 1 : block.aiSeparators [2]])) }
	if nActualArgCount >= 3 { a3 = strings.TrimSpace (string ((*pabyOutput) [block.aiSeparators [2] + 1 : block.aiSeparators [3]])) }
	if nActualArgCount >= 4 { a4 = strings.TrimSpace (string ((*pabyOutput) [block.aiSeparators [3] + 1 : block.aiSeparators [4]])) }

	if nActualArgCount == 1 && a1 == "" { nActualArgCount = 0 }

	if rows.m_stmt.m_con.hasDebugLog () {
		debugLog (fmt.Sprintf ("processEscapeBlock: abyFuncName=%v nActualArgCount=%v a1=%v a2=%v a3=%v a4=%v", string (abyFuncName), nActualArgCount, a1, a2, a3, a4))
	}

	// Invoke SYSJDBC.ESCRIGHT for pre-TD15.10 and use built-in RIGHT for TD 15.10 and later.
	bRight := rows.m_stmt.m_con.m_config.M_uDatabaseVersionNumber < 151000

	switch {
		case           escFunc (reATAN2     , [] int {2  }, join ("ATAN2(",a2,",",a1,")" /* swap args */                ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reCHAR      , [] int {1  }, join ("CHR(",a1,")"                                         ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reCONCAT    , [] int {2  }, join ("(",a1," || ",a2,")"                                  ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reCOT       , [] int {1  }, join ("(1 / TAN(",a1,"))"                                   ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reCURDATE   , [] int {0  }, join ("CURRENT_DATE"                                        ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reCURTIME   , [] int {0  }, join ("CURRENT_TIME(0)"                                     ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reDAYOFMONTH, [] int {1  }, join ("EXTRACT(DAY FROM ",a1,")"                            ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reDIFFERENCE, [] int {2  }, join ("(4 - EDITDISTANCE(SOUNDEX(",a1,"),SOUNDEX(",a2,")))" ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reHOUR      , [] int {1  }, join ("EXTRACT(HOUR FROM ",a1,")"                           ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reIFNULL    , [] int {2  }, join ("COALESCE(",a1,",",a2,")"                             ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reINSERT    , [] int {4  }, join ("SYSJDBC.ESCINSERT(",a1,",",a2,",",a3,",",a4,")"      ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reLCASE     , [] int {1  }, join ("LOWER(",a1,")"                                       ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reLEFT      , [] int {2  }, join ("SUBSTRING(",a1," FROM 1 FOR ",a2,")"                 ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reLENGTH    , [] int {1  }, join ("CHARACTER_LENGTH(",a1,")"                            ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reLOCATE    , [] int {2  }, join ("INDEX(",a2,",",a1,")" /* swap args */                ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reLOG       , [] int {1  }, join ("LN(",a1,")"                                          ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reLOG10     , [] int {1  }, join ("LOG(",a1,")"                                         ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reLTRIM     , [] int {1  }, join ("TRIM(LEADING FROM ",a1,")"                           ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reMINUTE    , [] int {1  }, join ("EXTRACT(MINUTE FROM ",a1,")"                         ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reMOD       , [] int {2  }, join ("(",a1," MOD ",a2,")"                                 ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reMONTH     , [] int {1  }, join ("EXTRACT(MONTH FROM ",a1,")"                          ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reNOW       , [] int {0  }, join ("CURRENT_TIMESTAMP(0)"                                ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (rePI        , [] int {0  }, join ("3.14159265358979323"                                 ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reRAND      , [] int {1,0}, join ("(CAST(RANDOM(0, 999999999) AS FLOAT) / 1000000000.0)"), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) : // discard arg
		case           escFunc (reREPEAT    , [] int {2  }, join ("SYSJDBC.ESCREPEAT(",a1,",",a2,")"                    ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reREPLACE   , [] int {3  }, join ("OREPLACE(",a1,",",a2,",",a3,")"                      ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case bRight && escFunc (reRIGHT     , [] int {2  }, join ("SYSJDBC.ESCRIGHT(",a1,",",a2,")"                     ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reRTRIM     , [] int {1  }, join ("TRIM(TRAILING FROM ",a1,")"                          ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reSECOND    , [] int {1  }, join ("EXTRACT(SECOND FROM ",a1,")"                         ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reSPACE     , [] int {1  }, join ("RPAD('', ",a1,")"                                    ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reSUBSTRING , [] int {3  }, join ("SUBSTRING(",a1," FROM ",a2," FOR ",a3,")"            ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reTRUNCATE  , [] int {2  }, join ("TRUNC(",a1,",",a2,")"                                ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reUCASE     , [] int {1  }, join ("UPPER(",a1,")"                                       ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reWEEK      , [] int {1  }, join ("EXTRACT(WEEK FROM ",a1,")"                           ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case           escFunc (reYEAR      , [] int {1  }, join ("EXTRACT(YEAR FROM ",a1,")"                           ), rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reCONVERT, [] int {2}, func () string {

				sType := strings.TrimPrefix (strings.ToUpper (a2), "SQL_")
				switch {
					case                    sType == "DOUBLE"        : sType = "DOUBLE PRECISION"
					case                    sType == "LONGVARBINARY" : sType = "VARBYTE(64000)" // LONG VARBYTE syntax is not supported, but VARBYTE(64000) is returned as LONG VARBYTE
					case                    sType == "LONGVARCHAR"   : sType = "LONG VARCHAR"
					case                    sType == "NUMERIC"       : sType = "NUMBER"
					case                    sType == "TINYINT"       : sType = "BYTEINT"
					case strings.HasPrefix (sType,   "BINARY"      ) : sType = "BYTE"    + strings.TrimPrefix (sType, "BINARY"   )
					case strings.HasPrefix (sType,   "VARBINARY"   ) : sType = "VARBYTE" + strings.TrimPrefix (sType, "VARBINARY")
				}

				return "CAST(" + a1 + " AS " + sType + ")"

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTIMESTAMPADD, [] int {3}, func () string {

				sUnit := strings.TrimPrefix (strings.ToUpper (a1), "SQL_TSI_")

				if sUnit == "MONTH" { return "ADD_MONTHS(" + a3 + "," + a2 + ")" }

				return "((" + a3 + ") + ("  + a2 + ") * INTERVAL '1' " + sUnit + ")"

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTIMESTAMPDIFF, [] int {3}, func () string {

				sUnit := strings.TrimPrefix (strings.ToUpper (a1), "SQL_TSI_")

				return "CAST((((" + a3 + ") - (" + a2 + ")) " + sUnit + ") AS INTEGER)"

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		// When shorter names match the beginning of longer names, the longer names must be tested first.

		case escFunc (reTERADATA_AMP_COUNT                      , [] int {0}, func () string { return format10Uint16 (rows.m_stmt.m_con.m_config.M_uAMPCount)              }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_ARRAY_TRANSFORM_OFF            , [] int {0}, func () string { rows.m_bUseArrayTransform = false ; return ""                               }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_ARRAY_TRANSFORM_ON             , [] int {0}, func () string { rows.m_bUseArrayTransform = true  ; return ""                               }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_AUTOCOMMIT_OFF                 , [] int {0}, func () string { rows.m_stmt.m_con.m_bAutoCommit = false ; return ""                         }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_AUTOCOMMIT_ON                  , [] int {0}, func () string { rows.m_stmt.m_con.m_bAutoCommit = true  ; return ""                         }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_AUTOCOMMIT                     , [] int {0}, func () string { return strconv.FormatBool (rows.m_stmt.m_con.m_bAutoCommit)                 }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_BYPASS_TX_COMMANDS             , [] int {0}, func () string { rows.m_bBypassTxCommands = true ; return ""                                 }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_COMMIT                         , [] int {0}, func () string { rows.m_bCommitRequest = true ; return ""                                    }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_CONNECTED                      , [] int {0}, func () string { return strconv.FormatBool (rows.m_stmt.m_con.m_bLoggedOn)                   }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_DATABASE_VERSION               , [] int {0}, func () string { return coalesce (rows.m_stmt.m_con.m_config.M_sDatabaseVersion, "none")     }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_DRIVER_VERSION                 , [] int {0}, func () string { return G_sVersionNumber                                                     }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_ERROR_TABLE_1_SUFFIX           , [] int {1}, func () string { rows.m_sErrorTable1Suffix = a1 ; return ""                                  }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_ERROR_TABLE_2_SUFFIX           , [] int {1}, func () string { rows.m_sErrorTable2Suffix = a1 ; return ""                                  }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_ERROR_TABLE_DATABASE           , [] int {1}, func () string { rows.m_sErrorTableDatabase = a1 ; return ""                                 }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_FAILFAST                       , [] int {0}, func () string { rows.m_bFailFast = true ; return ""                                         }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_FAKE_RESULT_SETS_OFF           , [] int {0}, func () string { rows.m_bFakeResultSets = false ; return ""                                  }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_FAKE_RESULT_SETS               , [] int {0}, func () string { rows.m_bFakeResultSets = true ; return ""                                   }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_GET_ERRORS                     , [] int {0}, func () string { rows.m_bGetErrors = true ; return ""                                        }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_GET_WARNINGS                   , [] int {0}, func () string { rows.m_bGetWarnings = true ; return ""                                      }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_GETLOGLEVEL                    , [] int {0}, func () string { return format10Uint64 (rows.m_stmt.m_con.m_params.M_uLog)                   }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_GO_DISTRIBUTION                , [] int {0}, func () string { return G_sGoDistribution                                                    }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_GO_RUNTIME                     , [] int {0}, func () string { return G_sGoRuntime                                                         }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_GOVERN_OFF                     , [] int {0}, func () string { rows.m_bIsGoverned = false ; return ""                                      }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_GOVERN_ON                      , [] int {0}, func () string { rows.m_bIsGoverned = true ; return ""                                       }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_LOGON_SEQUENCE_NUMBER          , [] int {0}, func () string { rows.m_bLSN = true ; return ""                                              }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_MANAGE_ERROR_TABLES_OFF        , [] int {0}, func () string { rows.m_bManageErrorTables = false ; return ""                               }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_MANAGE_ERROR_TABLES_ON         , [] int {0}, func () string { rows.m_bManageErrorTables = true ; return ""                                }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_NATIVESQL                      , [] int {0}, func () string { rows.m_bNoExecute = true ; return ""                                        }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_POSIXLT_OFF                    , [] int {0}, func () string { rows.m_bPOSIXlt = false ; return ""                                         }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_POSIXLT_ON                     , [] int {0}, func () string { rows.m_bPOSIXlt = true ; return ""                                          }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_PROGRAM_NAME                   , [] int {0}, func () string { return getOSProgramName (rows.m_stmt.m_con)                                 }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_REQUIRE_FASTEXPORT             , [] int {0}, func () string { rows.m_bRequireFastExport = true ; return ""                                }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_REQUIRE_FASTLOAD               , [] int {0}, func () string { rows.m_bRequireFastLoad = true ; return ""                                  }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_SEND_RUN_STARTUP_PARCEL        , [] int {0}, func () string { rows.m_bSendRunStartupParcel = true ; return ""                             }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_SESSION_NUMBER                 , [] int {0}, func () string { return format10Uint32 (rows.m_stmt.m_con.m_uSessionNumber)                  }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_TRY_FASTEXPORT                 , [] int {0}, func () string { rows.m_bTryFastExport = true ; return ""                                    }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_TRY_FASTLOAD                   , [] int {0}, func () string { rows.m_bTryFastLoad = true ; return ""                                      }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_UDT_TRANSFORMS_OFF             , [] int {0}, func () string { rows.m_bUseUDTTransforms = false ; return ""                                }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_UDT_TRANSFORMS_ON              , [] int {0}, func () string { rows.m_bUseUDTTransforms = true  ; return ""                                }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_UNTRUSTED                      , [] int {0}, func () string { rows.m_bUntrusted = true ; return ""                                        }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_VALUES_OFF                     , [] int {0}, func () string { rows.m_bTeradataValues = false ; return ""                                  }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :
		case escFunc (reTERADATA_VALUES_ON                      , [] int {0}, func () string { rows.m_bTeradataValues = true ; return ""                                   }, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_ERROR_QUERY_COUNT, [] int {1}, func () string {

				var u uint32
				u, err = parse10Uint32 (a1)
				if err != nil {
					err = rows.m_stmt.m_con.makeDriverError (err, 0, "Escape function TERADATA_ERROR_QUERY_COUNT requires numeric argument")
				} else {
					rows.m_uErrorQueryCount = u
				}

				return ""

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_ERROR_QUERY_INTERVAL, [] int {1}, func () string {

				var u uint32
				u, err = parse10Uint32 (a1)
				if err != nil {
					err = rows.m_stmt.m_con.makeDriverError (err, 0, "Escape function TERADATA_ERROR_QUERY_INTERVAL requires numeric argument")
				} else {
					rows.m_uErrorQueryInterval = u
				}

				return ""

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_READ_CSV, [] int {1}, func () string {

				rows.m_sCSVPathName = a1
				return ""

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_READ_PARQUET, [] int {1}, func () string {

				rows.m_sParquetPathName = a1
				return ""

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_WRITE_CSV, [] int {1}, func () string {

				rows.m_sExportPathName = a1
				rows.m_nFileExporterType = CSV_FILE_TYPE
				return ""

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_FIELD_SEP, [] int {1}, func () string {

				var bValid bool
				if bValid, rows.m_sFieldSeparator = validateFieldDelimiter (a1) ; ! bValid {
					err = rows.m_stmt.m_con.makeDriverError (err, 0, fmt.Sprintf ("Escape function TERADATA_FIELD_QUOTE has invalid argument %v", a1))
				}
				return ""

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_FIELD_QUOTE, [] int {1}, func () string {

				var bValid bool
				if bValid, rows.m_sFieldQuote = validateFieldDelimiter (a1) ; ! bValid {
					err = rows.m_stmt.m_con.makeDriverError (err, 0, fmt.Sprintf ("Escape function TERADATA_FIELD_QUOTE has invalid argument %v", a1))
				}
				return ""

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_SET_FILE_EXPORTER, [] int {/*any number*/ -1}, func () string {

			if rows.m_fileExporter == nil {
				if mgr, bPresent := rows.m_stmt.m_con.m_mapRequestToManager [sAllArgs] ; bPresent {

					switch typedval := mgr.(type) {

						case fastExportManager:

							fxpManager := typedval.Value ()
							rows.m_fileExporter = fxpManager.getFileExporter ()
							rows.m_nFileExporterType = fxpManager.getFileExporterType ()

					} // end switch
				} else {
					err = rows.m_stmt.m_con.makeDriverError (err, 0,
						fmt.Sprintf ("Escape function TERADATA_SET_FILE_EXPORTER is unable to retrieve the FastExportManager associated with the query '%v'",
							sAllArgs))
				}
			}

			return ""

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_SESSIONS, [] int {1}, func () string {

				var u uint32
				u, err = parse10Uint32 (a1)
				if err != nil {
					err = rows.m_stmt.m_con.makeDriverError (err, 0, "Escape function TERADATA_SESSIONS requires numeric argument")
				} else {
					rows.m_uSessions = u
				}

				return ""

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_ROLLBACK, [] int {0}, func () string {

				rows.m_bBypassTxCommands = true
				if rows.m_stmt.m_con.isAnsiMode () { return "ROLLBACK" }
				return "ABORT"

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_REQUEST_TIMEOUT, [] int {1}, func () string {

				var u uint32
				u, err = parse10Uint32 (a1)
				if err != nil {
					err = rows.m_stmt.m_con.makeDriverError (err, 0, "Escape function TERADATA_REQUEST_TIMEOUT requires numeric argument")
				} else {
					rows.m_uRequestTimeout = u
				}

				return ""

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_ROWS_HANDLE, [] int {1}, func () string {

				var u uint64
				u, err = strconv.ParseUint (a1, 10, 64)
				if err != nil {
					err = rows.m_stmt.m_con.makeDriverError (err, 0, "Escape function TERADATA_ROWS_HANDLE requires numeric argument")
				} else {
					rows.m_uRowsHandle = u
				}

				return ""

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_RPO, [] int {1}, func () string {

				if len (a1) == 1 {
					rows.m_byRPO = a1 [0]
				} else {
					err = rows.m_stmt.m_con.makeDriverError (nil, 0, fmt.Sprintf ("Invalid argument %v for escape function TERADATA_RPO", a1))
				}

				return ""

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_LOBSELECT, [] int {1}, func () string {

				if len (a1) == 1 {
					rows.m_byLOBSelect = a1 [0]
				} else {
					err = rows.m_stmt.m_con.makeDriverError (nil, 0, fmt.Sprintf ("Invalid argument %v for escape function TERADATA_LOBSELECT", a1))
				}

				return ""

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_CLOBTRANSLATE, [] int {1}, func () string {

				if len (a1) == 1 {
					rows.m_byCLOBTranslate = a1 [0]
				} else {
					err = rows.m_stmt.m_con.makeDriverError (nil, 0, fmt.Sprintf ("Invalid argument %v for escape function TERADATA_CLOBTRANSLATE", a1))
				}

				return ""

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_AGKR, [] int {1}, func () string {

				if len (a1) == 1 {
					rows.m_byAGKR = a1 [0]
				} else {
					err = rows.m_stmt.m_con.makeDriverError (nil, 0, fmt.Sprintf ("Invalid argument %v for escape function TERADATA_AGKR", a1))
				}

				return ""

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_PARAMETER, [] int {2}, func () string {

				nParamIndex, convErr := strconv.Atoi (a1)

				if convErr != nil {
					err = rows.m_stmt.m_con.makeDriverError (convErr, 0, fmt.Sprintf ("Invalid parameter index %v for escape function TERADATA_PARAMETER", a1))
					return ""
				}

				if rows.m_mapParameterDataTypes== nil {
					rows.m_mapParameterDataTypes = make (map [int] escParamDataTypeMetadata)
				}

				uPrecision, uScale, uIntervalDigits, uByteCount, sDataType := parseEscapeParameterMetadata (a2)

				if rows.m_stmt.m_con.hasDebugLog () {
					debugLog (fmt.Sprintf ("processEscapeBlock: fn teradata_parameter sDataType=%v uPrecision=%v uScale=%v uIntervalDigits=%v uByteCount=%v", sDataType, uPrecision, uScale, uIntervalDigits, uByteCount))
				}

				switch sDataType {

					case "BLOB AS LOCATOR", "LOCATOR(BLOB)" : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_BLOB_AS_LOCATOR}
					case "CLOB AS LOCATOR", "LOCATOR(CLOB)" : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_CLOB_AS_LOCATOR}
					case "XML AS LOCATOR" , "LOCATOR(XML)"  : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_XML_AS_LOCATOR}
					case "JSON AS LOCATOR", "LOCATOR(JSON)" : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_JSON_AS_LOCATOR}
					case "JSON"                             : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_JSON   , m_uByteCount : uByteCount}
					case "BYTE"                             : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_BYTE   , m_uByteCount : uByteCount}
					case "CHAR"                             : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_CHAR   , m_uByteCount : uByteCount}
					case "VARBYTE"                          : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_VARBYTE, m_uByteCount : uByteCount}
					case "VARCHAR"                          : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_VARCHAR, m_uByteCount : uByteCount}
					case "LONG VARBYTE"                     : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_LONG_VARBYTE}
					case "LONG VARCHAR"                     : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_LONG_VARCHAR}
					case "XML"                              : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_XML}
					case "BYTEINT"                          : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_BYTEINT}
					case "SMALLINT"                         : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_SMALLINT}
					case "INTEGER"                          : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_INTEGER}
					case "BIGINT"                           : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_BIGINT}
					case "FLOAT"                            : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_FLOAT}
					case "NUMBER"                           : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_NUMBER}
					case "DECIMAL"                          : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_DECIMAL, m_uPrecision : uPrecision, m_uScale : uScale}

					case "INTERVAL YEAR"                    : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_INTERVAL_YEAR            , m_uIntervalDigits : uIntervalDigits                    }
					case "INTERVAL YEAR TO MONTH"           : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_INTERVAL_YEAR_TO_MONTH   , m_uIntervalDigits : uIntervalDigits                    }
					case "INTERVAL MONTH"                   : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_INTERVAL_MONTH           , m_uIntervalDigits : uIntervalDigits                    }
					case "INTERVAL DAY"                     : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_INTERVAL_DAY             , m_uIntervalDigits : uIntervalDigits                    }
					case "INTERVAL DAY TO HOUR"             : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_INTERVAL_DAY_TO_HOUR     , m_uIntervalDigits : uIntervalDigits                    }
					case "INTERVAL DAY TO MINUTE"           : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_INTERVAL_DAY_TO_MINUTE   , m_uIntervalDigits : uIntervalDigits                    }
					case "INTERVAL DAY TO SECOND"           : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_INTERVAL_DAY_TO_SECOND   , m_uIntervalDigits : uIntervalDigits, m_uScale : uScale }
					case "INTERVAL HOUR"                    : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_INTERVAL_HOUR            , m_uIntervalDigits : uIntervalDigits                    }
					case "INTERVAL HOUR TO MINUTE"          : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_INTERVAL_HOUR_TO_MINUTE  , m_uIntervalDigits : uIntervalDigits                    }
					case "INTERVAL HOUR TO SECOND"          : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_INTERVAL_HOUR_TO_SECOND  , m_uIntervalDigits : uIntervalDigits, m_uScale : uScale }
					case "INTERVAL MINUTE"                  : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_INTERVAL_MINUTE          , m_uIntervalDigits : uIntervalDigits                    }
					case "INTERVAL MINUTE TO SECOND"        : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_INTERVAL_MINUTE_TO_SECOND, m_uIntervalDigits : uIntervalDigits, m_uScale : uScale }
					case "INTERVAL SECOND"                  : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_INTERVAL_SECOND          , m_uIntervalDigits : uIntervalDigits, m_uScale : uScale }

					case "PERIOD(DATE)"                     : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_PERIOD_DATE                                        }
					case "PERIOD(TIME)"                     : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_PERIOD_TIME                    , m_uScale : uScale }
					case "PERIOD(TIME WITH TIME ZONE)"      : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_PERIOD_TIME_WITH_TIME_ZONE     , m_uScale : uScale }
					case "PERIOD(TIMESTAMP)"                : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_PERIOD_TIMESTAMP               , m_uScale : uScale }
					case "PERIOD(TIMESTAMP WITH TIME ZONE)" : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_PERIOD_TIMESTAMP_WITH_TIME_ZONE, m_uScale : uScale }
					case "DATE ANSI"                        : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_DATE_ANSI                                          }
					case "DATE INTEGER"                     : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_DATE_INTEGER                                       }
					case "TIME"                             : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_TIME                    , m_uScale : uScale }
					case "TIME WITH TIME ZONE"              : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_TIME_WITH_TIME_ZONE     , m_uScale : uScale }
					case "TIMESTAMP"                        : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_TIMESTAMP               , m_uScale : uScale }
					case "TIMESTAMP WITH TIME ZONE"         : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_TIMESTAMP_WITH_TIME_ZONE, m_uScale : uScale }

					case "DATASET STORAGE FORMAT AVRO"      : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_DATASET_AVRO            , m_uByteCount : uByteCount }
					case "DATASET STORAGE FORMAT CSV"       : rows.m_mapParameterDataTypes [nParamIndex] = escParamDataTypeMetadata {m_uCookedDataType : T_DATASET_CSV             , m_uByteCount : uByteCount }

					default: err = rows.m_stmt.m_con.makeDriverError (nil, 0,
						fmt.Sprintf ("Invalid data type '%v' (parameter index %v) for escape function TERADATA_PARAMETER",
							sDataType,
							a1))
				}

				return ""

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_SOCKET_INFO, [] int {0}, func () string {

				return "socket orig=" + rows.m_stmt.m_con.m_params.M_sHostName +
					fmt.Sprintf (" local=%v"             , safeLocalAddr (rows.m_stmt.m_con.m_socket)) +
					fmt.Sprintf (" remote=%v"            , safeRemoteAddr (rows.m_stmt.m_con.m_socket)) +
					fmt.Sprintf (" connectattempts=%v"   , rows.m_stmt.m_con.m_nConnectAttempts) +
					fmt.Sprintf (" connectfailures=%v"   , rows.m_stmt.m_con.m_nConnectFailures) +
					fmt.Sprintf (" reconnectattempts=%v" , rows.m_stmt.m_con.m_nReconnectAttempts) +
					fmt.Sprintf (" tokencachestatus=%v"  , rows.m_stmt.m_con.m_extra.m_sTokenCacheStatus) +
					fmt.Sprintf (" tokenreusecount=%v"   , rows.m_stmt.m_con.m_extra.m_sTokenReuseCount) +
					fmt.Sprintf (" recoverable=%v"       , rows.m_stmt.m_con.isRNPActive ()) +
					fmt.Sprintf (" redrive=%v"           , rows.m_stmt.m_con.isRedriveActive ())

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_WEB_SERVICE, [] int {2, 3}, func () string {

				var sResponse string ; sResponse, err = rows.webService (a1, a2, a3) ; return sResponse

			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

		case escFunc (reTERADATA_PROVIDE, [] int {1}, func () string {

				switch strings.ToUpper (a1) {

					case "CONFIG_RESPONSE"               : return rows.safeMarshal (rows.m_stmt.m_con.m_config)
					case "CONNECTION_ID"                 : return rows.m_stmt.m_con.m_sConnectionID
					case "DEFAULT_CONNECTION"            : return "false"
					case "DHKE"                          : return itoa (rows.m_stmt.m_con.m_nDHKE)
					case "GATEWAY_CONFIG"                : return rows.safeMarshal (rows.m_stmt.m_con.m_gtwConfig)
					case "GOVERNED"                      : return strconv.FormatBool (rows.m_stmt.m_con.m_params.M_bGovern)
					case "HOST_ID"                       : return format10Uint16 (rows.m_stmt.m_con.m_uHostID)
					case "JAVA_CHARSET_NAME"             : return "UTF8"
					case "LOB_SUPPORT"                   : return strconv.FormatBool (rows.m_bUseLOB)
					case "LOCAL_ADDRESS"                 : return hostFromAddr (safeLocalAddr (rows.m_stmt.m_con.m_socket))
					case "LOCAL_PORT"                    : return portFromAddr (safeLocalAddr (rows.m_stmt.m_con.m_socket))
					case "ORIGINAL_HOSTNAME"             : return rows.m_stmt.m_con.m_params.M_sHostName
					case "REDRIVE_ACTIVE"                : return strconv.FormatBool (rows.m_stmt.m_con.m_bRedriveActive)
					case "REMOTE_ADDRESS"                : return rows.m_stmt.m_con.m_sConnectedHostName + "/" + hostFromAddr (safeRemoteAddr (rows.m_stmt.m_con.m_socket))
					case "REMOTE_PORT"                   : return portFromAddr (safeRemoteAddr (rows.m_stmt.m_con.m_socket))
					case "REQUEST_SCOPE_COLUMN_NAME_OFF" : rows.m_bColumnName = false ; return ""
					case "REQUEST_SCOPE_LOB_SUPPORT_OFF" : rows.m_bUseLOB = false ; return ""
					case "REQUEST_SCOPE_RECEIVE_ONLY"    : rows.m_bReceiveOnly = true ; return ""
					case "REQUEST_SCOPE_REFRESH_RSMD"    : rows.m_byRPO = 'B' ; return ""
					case "REQUEST_SCOPE_SEND_ONLY"       : rows.m_bSendOnly = true ; return ""
					case "REQUEST_SCOPE_SIP_SUPPORT_OFF" : rows.m_bWantSIP = false ; rows.m_bSendSIP = false ; return ""
					case "RNP_ACTIVE"                    : return strconv.FormatBool (rows.m_stmt.m_con.m_bRNPActive)
					case "SESSION_CHARSET_CODE"          : return format10Uint8 (CHARSET_CODE_UTF8)
					case "SESSION_CHARSET_NAME"          : return "UTF8"
					case "SIP_SUPPORT"                   : return strconv.FormatBool (rows.m_bWantSIP)
					case "TOKEN"                         : var sToken string ; sToken, err = rows.provideToken () ; return sToken
					case "TRANSACTION_MODE"              : if rows.m_stmt.m_con.isAnsiMode () { return "ANSI" } ; return "TERA"
					case "USES_CHECK_WORKLOAD"           : return strconv.FormatBool (rows.m_stmt.m_con.m_config.M_uCheckWorkloadSupport >= 1 && (rows.m_stmt.m_con.m_params.M_bGovern || rows.m_stmt.m_con.m_config.M_uTDWMFailFastSupport >= 1))

					default:
						err = rows.m_stmt.m_con.makeDriverError (nil, 0, fmt.Sprintf ("Unrecognized argument %v for escape function TERADATA_PROVIDE", a1))
						return ""
				}
			}, rows, block.iBlockStart, abyFuncName, nActualArgCount, pabyOutput, &err) :

	} // end switch

	if err != nil { return }

	return

} // end func processEscapeBlock

func escFunc (re *regexp.Regexp, anExpectedArgCounts [] int, closure func () string, rows *TeradataRows, iBlockStart int, abyFuncName [] byte, nActualArgCount int, pabyOutput *[] byte, perr *error) (bOutput bool) {

	ai := re.FindSubmatchIndex (abyFuncName)
	if ai == nil { return }

	var bValidArgCount bool
	for i := 0 ; ! bValidArgCount && i < len (anExpectedArgCounts) ; i++ {
		if anExpectedArgCounts [i] == nActualArgCount || anExpectedArgCounts [i] == /*any number*/ -1 {
			bValidArgCount = true
		}
	}

	if ! bValidArgCount {

		*perr = rows.m_stmt.m_con.makeDriverError (nil, 0,
			fmt.Sprintf ("Escape function %s expected %v argument(s) but %v argument(s) were provided",
				abyFuncName,
				anExpectedArgCounts [0],
				nActualArgCount))

		bOutput = true
		return
	}

	*pabyOutput = append ((*pabyOutput) [ : iBlockStart], closure () ...)
	bOutput = true
	return

} // end func escFunc

func join (as ... string) func () string {

	return func () string { return strings.Join (as, "") }

} // end func join

func parseEscapeParameterMetadata (sType string) (uPrecision uint8, uScale uint8, uIntervalDigits uint16, uByteCount uint64, sBaseType string) {

	rgxRemove := regexp.MustCompile(` ?\(([0-9, ]+)\)`)
	sBaseType = rgxRemove.ReplaceAllString (sType, "")

	sBaseType = strings.Replace (sBaseType, "PERIOD (", "PERIOD(", 1)

	switch sBaseType {

		case "DECIMAL" :

			// DECIMAL [(PRECISION, SCALE)]

			asPrecAndScale := regexp.MustCompile (`([0-9]+)`).FindAllString (sType, -1)

			for i, sValue := range asPrecAndScale {

				n, _ := strconv.Atoi (sValue)

				switch i {
					case 0 : uPrecision = uint8 (n)
					case 1 : uScale = uint8 (n)
				}
			} // end for

		case "BYTE"  ,
			"CHAR"   ,
			"VARBYTE",
			"VARCHAR",
			"JSON"   ,
			"DATASET STORAGE FORMAT CSV"  ,
			"DATASET STORAGE FORMAT AVRO" :

			// BYTE [(LENGTH)]
			// CHAR [(LENGTH)]
			// VARBYTE [(LENGTH)]
			// VARCHAR [(LENGTH)]
			// JSON [(LENGTH)]
			// DATASET [(LENGTH)] STORAGE FORMAT AVRO
			// DATASET [(LENGTH)] STORAGE FORMAT CSV

			asByteCount := regexp.MustCompile ("[0-9]+").FindAllString (sType, -1)

			if len (asByteCount) > 0 && len (asByteCount [0]) > 0 {
				n, _ := strconv.Atoi (asByteCount [0])
				uByteCount = uint64 (n)
			}

		case "TIME"                            ,
			"TIME WITH TIME ZONE"              ,
			"TIMESTAMP"                        ,
			"TIMESTAMP WITH TIME ZONE"         ,
			"PERIOD(TIME)"                     ,
			"PERIOD(TIME WITH TIME ZONE)"      ,
			"PERIOD(TIMESTAMP)"                ,
			"PERIOD(TIMESTAMP WITH TIME ZONE)" :

			// TIME/TIMESTAMP [(scale)] [WITH TIME ZONE]
			// PERIOD(TIME/TIMESTAMP [(scale)] [WITH TIME ZONE])

			uScale = 6

			asScale := regexp.MustCompile (`([0-9]+)`).FindAllString (sType, -1)

			if len (asScale) > 0 {
				n, _ := strconv.Atoi (asScale [0])
				uScale = uint8 (n)
			}

		case "INTERVAL SECOND" :

			// INTERVAL SECOND [(IntervalDigits [,scale])]

			uIntervalDigits = 2
			uScale     = 6

			asDigitsAndScale := regexp.MustCompile (`([0-6])`).FindAllString (sType, -1)

			for i, sValue := range asDigitsAndScale {

				n, _ := strconv.Atoi (sValue)

				switch i {
					case 0 : uIntervalDigits = uint16 (n)
					case 1 : uScale = uint8 (n)
				}

			}

		case "INTERVAL DAY TO SECOND"   ,
			"INTERVAL HOUR TO SECOND"   ,
			"INTERVAL MINUTE TO SECOND" :

			// INTERVAL...TO SECOND [(scale)]
			// Just retrieve the scale if one is specified, if not, the default value is 6

			uScale = 6

			rgxScale := regexp.MustCompile (`(?:.*)TO SECOND ?\(([0-6])\)`)
			asScale  := rgxScale.FindStringSubmatch (sType)

			if len (asScale) > 1 && len (asScale [1]) > 0 {
				n, _ := strconv.Atoi (asScale [1])
				uScale = uint8 (n)
			}

			// we want to fall through to check if a IntervalDigits value was specified
			fallthrough

		case "INTERVAL YEAR"            ,
			"INTERVAL YEAR TO MONTH"    ,
			"INTERVAL MONTH"            ,
			"INTERVAL DAY"              ,
			"INTERVAL DAY TO HOUR"      ,
			"INTERVAL DAY TO MINUTE"    ,
			"INTERVAL HOUR"             ,
			"INTERVAL HOUR TO MINUTE"   ,
			"INTERVAL MINUTE"           :

			// Can be 'INTERVAL <type> [(intervalDigits)] TO SECOND [(scale)]' or 'INTERVAL <type> [(intervalDigits)] [TO <type>]'
			// but will not process 'INTERVAL SECOND [(intervalDigits [,scale])]' here. This is handled above.
			// This makes sure we don't pick up the second's scale as the interval digits if no interval digits value is specified

			uIntervalDigits = 2

			var rIntervalDigits = regexp.MustCompile (`INTERVAL [A-Z]* ?\(([0-4])\).*`)
			asIntervalDigits := rIntervalDigits.FindStringSubmatch (sType)

			if len (asIntervalDigits) > 1 && len (asIntervalDigits [1]) > 0 {
				n, _ := strconv.Atoi (asIntervalDigits [1])
				uIntervalDigits = uint16 (n)

			}
	} // end switch sBaseIntervalType

	return

} // end func parseEscapeParameterMetadata

func validateFieldDelimiter (s string) (bool, string) {

	if s == "''''" { return true, "'"}
	if s == "\"\"\"\"" { return true, "\""}

	if utf8.RuneCountInString (s) == 3 {
		r := [] rune (s)
		if (r [0] == '\'' || r [0] == '"') && (r [2] == '\'' || r [2] == '"') && (r [0] == r [2]) {
			s = string (r [1])
			return s != "\r" && s != "\n" && utf8.RuneCountInString (s) == 1 && utf8.ValidRune ([] rune (s) [0]) && [] rune (s) [0] != utf8.RuneError, s
		}
	}

	return false, s

} // end validateFieldDelimiter
