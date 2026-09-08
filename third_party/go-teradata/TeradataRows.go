// Copyright 2017-2022 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var _ driver.Rows                             = &TeradataRows {} // implements driver.Rows
var _ driver.RowsColumnTypeDatabaseTypeName   = &TeradataRows {} // implements driver.RowsColumnTypeDatabaseTypeName
var _ driver.RowsColumnTypeLength             = &TeradataRows {} // implements driver.RowsColumnTypeLength
var _ driver.RowsColumnTypeNullable           = &TeradataRows {} // implements driver.RowsColumnTypeNullable
var _ driver.RowsColumnTypePrecisionScale     = &TeradataRows {} // implements driver.RowsColumnTypePrecisionScale
var _ driver.RowsColumnTypeScanType           = &TeradataRows {} // implements driver.RowsColumnTypeScanType
var _ driver.RowsNextResultSet                = &TeradataRows {} // implements driver.RowsNextResultSet
var _ fmt.Stringer                            = &TeradataRows {} // implements fmt.Stringer

type TeradataRows struct {

	m_stmt                     *teradataStatement
	m_bFakeResultSets          bool
	m_bSendRunStartupParcel    bool
	m_bColumnName              bool
	m_bPOSIXlt                 bool
	m_sNativeSQL               string
	m_uRequestTimeout          uint32
	m_uRowsHandle              uint64
	m_byRPO                    byte
	m_byLOBSelect              byte
	m_byCLOBTranslate          byte
	m_byAGKR                   byte
	m_bFailFast                bool
	m_bUntrusted               bool
	m_bFreedSpool              bool
	m_bClosed                  bool
	m_bUseLOB                  bool
	m_bWantSIP                 bool
	m_bSendSIP                 bool
	m_bUseUDTTransforms        bool
	m_bUseArrayTransform       bool
	m_bWantBigRows             bool
	m_mapParameterDataTypes    map [int] escParamDataTypeMetadata
	m_uRequestLimit            uint32
	m_uResponseLimit           uint32
	m_uRequestNumber           uint32
	m_uErrorQueryCount         uint32
	m_uErrorQueryInterval      uint32
	m_sErrorTable1Suffix       string
	m_sErrorTable2Suffix       string
	m_sErrorTableDatabase      string
	m_bManageErrorTables       bool
	m_bGetErrors               bool
	m_bGetWarnings             bool
	m_bLSN                     bool
	m_uSessions                uint32
	m_bRequireFastLoad         bool
	m_bTryFastLoad             bool
	m_bRequireFastExport       bool
	m_bTryFastExport           bool
	m_fastexportManager        fastExportManager
	m_bReceiveOnly             bool
	m_bSendOnly                bool
	m_sCSVPathName             string
	m_sParquetPathName         string
	m_fileImporter             fileImporter
	m_sExportPathName          string
	m_fileExporter             fileExporter
	m_nFileExporterType        int
	m_sFieldSeparator          string
	m_sFieldQuote              string
	m_bIsGoverned              bool
	m_bTeradataValues          bool

	m_uStatementNumber         uint32
	m_uActivityType            uint16
	m_uActivityCount           uint64
	m_uWarningCode             uint16
	m_sRawWarningMsg           string
	m_sWarningMsg              string
	m_aParamMetadata           [] columnMetadata
	m_aColumnMetadata          [] columnMetadata
	m_asColumnNames            [] string
	m_atColumnScanTypes        [] reflect.Type
	m_bNoExecute               bool
	m_nFakeState               int
	m_bCommitRequest           bool
	m_bBypassTxCommands        bool
	m_bProcessingBatchRequest  bool
	m_uBatchSegmentStartRow    uint16
	m_batchStatementErrorMap   map [uint16] batchStatementError // map<statement number uint16, errorInfo batchStatementError>
	m_bAbortSent               bool
	m_abyContinueContextBody   [] byte

	m_abyCurrentParcelHeader   [] byte

} // end type TeradataRows

var g_mutexRowsHandles sync.RWMutex // protects g_mapRowsHandles
var g_mapRowsHandles = make (map [uint64] *TeradataRows)

const ( turnedOff = iota ; returnRow ; resultDone ) // values for m_nFakeState

type columnMetadata struct {

	m_bHasPrecScale             bool
	m_bHasVarLength             bool

	// available with PrepInfo, PrepInfoX, and StatementInfo

	M_uRawDataType              uint16    `json:"RawDataType"`
	M_uCookedDataType           uint16    `json:"CookedDataType"`
	M_bNullable                 bool      `json:"Nullable"`
	M_sTypeName                 string    `json:"TypeName"`
	M_sName                     string    `json:"Name"`
	M_sTitle                    string    `json:"Title"`
	M_sFormat                   string    `json:"Format"`
	M_uByteCount                uint64    `json:"ByteCount"`
	M_uPrecision                uint8     `json:"Precision"`
	M_uScale                    uint8     `json:"Scale"`

	// available with PrepInfoX and StatementInfo

	M_uServerCharsetCode        uint8     `json:"ServerCharsetCode"`
	M_bCaseSensitive            bool      `json:"CaseSensitive"`

	// available with StatementInfo

	M_sDatabaseName             string    `json:"DatabaseName"`
	M_sObjectName               string    `json:"ObjectName"`
	M_uPosition                 uint16    `json:"Position"`
	M_sAsClauseName             string    `json:"AsClauseName"`
	M_sDefaultValue             string    `json:"DefaultValue"`
	M_bIdentityColumn           bool      `json:"IdentityColumn"`
	M_bDefinitelyWritable       bool      `json:"DefinitelyWritable"`
	M_bMayBeNull                bool      `json:"MayBeNull"`
	M_bSearchable               bool      `json:"Searchable"`
	M_bWritable                 bool      `json:"Writable"`
	M_uUDTKind                  uint16    `json:"UDTKind"`
	M_sUDTName                  string    `json:"UDTName"`
	M_sDataTypeMiscInfo         string    `json:"DataTypeMiscInfo"`
	M_uIntervalDigits           uint16    `json:"IntervalDigits"`
	M_uMaxCharacterCount        uint64    `json:"MaxCharacterCount"`
	M_bSigned                   bool      `json:"Signed"`
	M_bKeyColumn                bool      `json:"KeyColumn"`
	M_bUnique                   bool      `json:"Unique"`
	M_bExpression               bool      `json:"Expression"`
	M_bSortable                 bool      `json:"Sortable"`
	M_uSPParameterDirection     uint8     `json:"SPParameterDirection"`
	M_uStructDepth              uint16    `json:"StructDepth"`
	M_uTemporal                 byte      `json:"Temporal"`
	M_sAttributeName            string    `json:"AttributeName"`
	M_uServerDataType           uint16    `json:"ServerDataType"`
	M_nArrayNumberOfDimensions  int       `json:"ArrayNumberOfDimensions"`
	M_auArrayMaxCardinalities   [] uint32 `json:"ArrayMaxCardinalities"`
	M_auArrayLowerBounds        [] uint32 `json:"ArrayLowerBounds"`
	M_sCatalogName              string    `json:"CatalogName"`

} // end type columnMetadata

type bindMetadata struct {

	m_uCookedDataType    uint16
	m_uNullableDataType  uint16
	m_uByteCount         uint64
	m_uPrecision         uint8
	m_uScale             uint8
	m_uIntervalDigits    uint16
	m_uServerDataType    uint16

} // end type bindMetadata

type escParamDataTypeMetadata struct {
	m_uCookedDataType   uint16
	m_uIntervalDigits   uint16
	m_uByteCount        uint64
	m_uPrecision        uint8
	m_uScale            uint8
}

type batchStatementError struct {
	m_uErrorCode       uint16
	m_sErrorMsg        string
}

// Note: This pointer-receiver method will not work with a value inside an interface, which we do not expect or intend.

func (rows *TeradataRows) String () string { // satisfies fmt.Stringer

	return fmt.Sprintf ("%v rows=%p noex=%v fake=%v freed=%v closed=%v req=%v stmt=%v acttype=%v actcnt=%v params=%v cols=%v",
		rows.m_stmt, rows, rows.m_bNoExecute, rows.m_nFakeState, rows.m_bFreedSpool, rows.m_bClosed, rows.m_uRequestNumber, rows.m_uStatementNumber, rows.m_uActivityType, rows.m_uActivityCount,
		len (rows.m_aParamMetadata), len (rows.m_aColumnMetadata))

} // end method String

func (rows *TeradataRows) keepResponse () bool {

	return rows.m_bUseLOB && rows.m_byLOBSelect != 'I' // keep response only if we are using LOB locators

} // end method keepResponse

func newTeradataRows (stmt *teradataStatement, ctx context.Context, aBindValues [] driver.NamedValue, bResultSetExpected bool) (rows *TeradataRows, err error) {

	if stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter newTeradataRows %v bind=%v", stmt, aBindValues))
		defer func () { traceLog (fmt.Sprintf ("< leave newTeradataRows %v err=%v", rows, err)) } ()
	}

	defer func () { // catch and wrap any panic
		if ex := recover () ; ex != nil {
			err = rows.m_stmt.m_con.makeDriverError (err, 0, formatRecover ("Cannot initiate database request", ex)) // preserve existing err (if any) as cause
		}
	} ()

	bPartitionDBCCONS    := strings.EqualFold (stmt.m_con.m_params.M_sPartition, "DBCCONS")
	bPartitionMonitor    := strings.EqualFold (stmt.m_con.m_params.M_sPartition, "MONITOR")
	bPartitionFastLoad   := strings.EqualFold (stmt.m_con.m_params.M_sPartition, "FASTLOAD")
	bPartitionFastExport := strings.EqualFold (stmt.m_con.m_params.M_sPartition, "EXPORT")

	rows = &TeradataRows {
		m_stmt                    : stmt,
		m_bFakeResultSets         : stmt.m_con.m_params.M_bFakeResultSets,
		m_bColumnName             : stmt.m_con.m_params.M_bColumnName,
		m_bPOSIXlt                : stmt.m_con.m_params.M_bPOSIXlt,
		m_uRequestTimeout         : stmt.m_con.m_params.M_uRequestTimeout,
		m_byRPO                   : 'B', // PrepareAndExecute
		m_byLOBSelect             : 'I', // Inline
		m_byCLOBTranslate         : 'L', // Locked
		m_bUseLOB                 : stmt.m_con.m_params.M_bLOBSupport && stmt.m_con.m_config.M_uMultipartParcelAndLOBSupport > 0 && bResultSetExpected && ! bPartitionDBCCONS && ! bPartitionMonitor,
		m_bWantSIP                : stmt.m_con.m_params.M_bSIPSupport && stmt.m_con.m_config.M_uSIPResponseSupport > 0,
		m_bSendSIP                : stmt.m_con.m_params.M_bSIPSupport && stmt.m_con.m_config.M_uSIPRequestSupport > 0 && ! bPartitionDBCCONS && ! bPartitionMonitor,
		m_bUseUDTTransforms       : true, // default to using transforms
		m_bUseArrayTransform      : true, // default to using transforms
		m_bWantBigRows            : minUint32 (stmt.m_con.m_params.M_uMaxMessageBodySize, stmt.m_con.m_config.M_uMaxAvailableBytesInResponseRow) > 64768,
		m_uRequestLimit           : minUint32 (stmt.m_con.m_params.M_uMaxMessageBodySize, stmt.m_con.m_config.M_uMaxRequestSizeInBytes),
		m_uResponseLimit          : minUint32 (stmt.m_con.m_params.M_uMaxMessageBodySize, stmt.m_con.m_config.M_uMaxResponseSizeInBytes),
		m_bCommitRequest          : stmt.m_con.m_bAutoCommit && ! bPartitionDBCCONS && ! bPartitionMonitor,
		m_bBypassTxCommands       : bPartitionDBCCONS || bPartitionMonitor,
		m_uErrorQueryCount        : stmt.m_con.m_params.M_uErrorQueryCount,
		m_uErrorQueryInterval     : stmt.m_con.m_params.M_uErrorQueryInterval,
		m_sErrorTable1Suffix      : stmt.m_con.m_params.M_sErrorTable1Suffix,
		m_sErrorTable2Suffix      : stmt.m_con.m_params.M_sErrorTable2Suffix,
		m_sErrorTableDatabase     : stmt.m_con.m_params.M_sErrorTableDatabase,
		m_bManageErrorTables      : stmt.m_con.m_params.M_bManageErrorTables,
		m_uSessions               : stmt.m_con.m_params.M_uSessions,
		m_sFieldQuote             : stmt.m_con.m_params.M_sFieldQuote,
		m_sFieldSeparator         : stmt.m_con.m_params.M_sFieldSeparator,
		m_bIsGoverned             : stmt.m_con.m_params.M_bGovern,
		m_bTeradataValues         : stmt.m_con.m_params.M_bTeradataValues,
	}

	if rows.m_stmt.m_con.hasDebugLog () {
		debugLog (fmt.Sprintf ("newTeradataRows: m_bUseLOB=%v m_bWantSIP=%v m_bSendSIP=%v m_uResponseLimit=%v", rows.m_bUseLOB, rows.m_bWantSIP, rows.m_bSendSIP, rows.m_uResponseLimit))
	}

	err = toNativeSQL (rows)
	if err != nil { return }

	if len (aBindValues) != 0 {

		if bPartitionDBCCONS {
			err = rows.m_stmt.m_con.makeDriverError (nil, 0, "Bound parameter values cannot be specified when using the DBCCONS partition")
			return
		}

		if rows.m_sCSVPathName != "" {
			err = rows.m_stmt.m_con.makeDriverError (nil, 540, "Bound parameter values cannot be specified when using escape function TERADATA_READ_CSV")
			return
		}

		if rows.m_sParquetPathName != "" {
			err = rows.m_stmt.m_con.makeDriverError (nil, 540, "Bound parameter values cannot be specified when using escape function TERADATA_READ_PARQUET")
			return
		}

		// future expansion - process additional file types here

	} // end if aBindValues

	if rows.m_stmt.m_con.hasDebugLog () {
		debugLog (fmt.Sprintf ("newTeradataRows: m_uRowsHandle=%v m_uRequestTimeout=%v m_bUseLOB=%v m_bWantSIP=%v m_byRPO=%c m_byLOBSelect=%c m_sNativeSQL=%v", rows.m_uRowsHandle, rows.m_uRequestTimeout, rows.m_bUseLOB, rows.m_bWantSIP, rows.m_byRPO, rows.m_byLOBSelect, rows.m_sNativeSQL))
	}

	if rows.m_uRowsHandle != 0 {
		g_mutexRowsHandles.Lock () // write lock
		g_mapRowsHandles [rows.m_uRowsHandle] = rows
		g_mutexRowsHandles.Unlock ()
	}

	if rows.m_bNoExecute {
		rows.m_bFreedSpool = true
		rows.m_nFakeState = returnRow
		return
	}

	if rows.m_stmt.m_con.hasDebugLog () {
		debugLog (fmt.Sprintf ("newTeradataRows: m_uRowsHandle=%v", rows.m_uRowsHandle))
	}

	if rows.m_uRequestTimeout > 0 {
		var cancelFunc context.CancelFunc
		ctx, cancelFunc = context.WithTimeout (ctx, time.Duration (rows.m_uRequestTimeout) * time.Second)
		defer cancelFunc () // release the derived context if we finish before the timeout expires, leaving the parent context intact
	}

	if rows.m_sCSVPathName != "" {

		rows.m_fileImporter, err = newFileImporter (rows.m_sCSVPathName, CSV_FILE_TYPE, rows.m_sFieldSeparator, rows.m_sFieldQuote, rows.m_mapParameterDataTypes)
		if err != nil {
			err = rows.m_stmt.m_con.makeDriverError (err, 542, fmt.Sprintf ("Escape function TERADATA_READ_CSV specifies an inaccessible file %v", rows.m_sCSVPathName))
			return
		}

		defer rows.m_fileImporter.close ()
	}

	if rows.m_sParquetPathName != "" {

		rows.m_fileImporter, err = newFileImporter (rows.m_sParquetPathName, PARQUET_FILE_TYPE, "", "", rows.m_mapParameterDataTypes)
		if err != nil {
			err = rows.m_stmt.m_con.makeDriverError (err, 542, fmt.Sprintf ("Escape function TERADATA_READ_PARQUET specifies an inaccessible or corrupted file %v", rows.m_sParquetPathName))
			return
		}

		defer rows.m_fileImporter.close ()
	}

	// future expansion - process additional file types here

	var bBatchInsert bool
	var aBindMetadata [] bindMetadata

	if rows.m_bTryFastLoad || rows.m_bRequireFastLoad {

		if rows.m_stmt.m_con.hasDebugLog () {
			debugLog (fmt.Sprintf ("m_mapRequestToManager=%v", rows.m_stmt.m_con.m_mapRequestToManager))
		}

		var fldManager fastLoadManager
		switch typedval := rows.m_stmt.m_con.m_mapRequestToManager [strings.TrimSpace (rows.m_sNativeSQL)].(type) {
			case fastLoadManager:
				fldManager = typedval.Value ()
			default:
				fldManager = nil
		}

		if fldManager == nil || fldManager.hadWarningsAtNew () || ! fldManager.isConnected () {
			if rows.m_stmt.m_con.hasDebugLog () {
				debugLog (fmt.Sprintf ("m_mapRequestToManager[%v]=%v", rows.m_sNativeSQL, fldManager))
			}

			fldManager, err = newFastLoadManager(rows, ctx, aBindValues)
			if err != nil { return }

			fldManager.setWarningsAtNew (len (fldManager.getWarnings ()) > 0)
			rows.m_stmt.m_con.m_mapRequestToManager [strings.TrimSpace (rows.m_sNativeSQL)] = fldManager
		}

		// fldManager is non-nil at this point

		if ! fldManager.hadWarningsAtNew () {

			delete (fldManager.getAppCon ().m_mapRequestToErrors, rows.m_sNativeSQL) // clear errors
			fldManager.setWarnings (make ([] string, 0)) // clear warnings

			if rows.m_stmt.m_con.hasDebugLog () {
				debugLog (fmt.Sprintf ("m_mapRequestToManager[%v]=%v", rows.m_sNativeSQL, rows.m_stmt.m_con.m_mapRequestToManager [rows.m_sNativeSQL]))
			}

			if rows.m_fileImporter != nil {
				fldManager.setFileImporter (rows.m_fileImporter)
				err = fldManager.executeInsert (&rows.m_uActivityCount, nil, ctx)
			} else {
				err = fldManager.executeInsert (&rows.m_uActivityCount, aBindValues, ctx)
			}
			if err != nil { return }

			bFastLoadCompatible := len (fldManager.getWarningsAtTypeCast ()) == 0
			if bFastLoadCompatible { // we are all done
				rows.m_bFreedSpool = true
				return
			}

			// The SQL request was not compatible with FastLoad, so we will proceed to execute it as regular SQL.
			bBatchInsert = fldManager.isBatchInsert ()
			aBindMetadata = fldManager.getBindMetadata ()
		}

	} else if rows.m_bTryFastExport || rows.m_bRequireFastExport {

		if rows.m_stmt.m_con.hasDebugLog () {
			debugLog (fmt.Sprintf ("m_mapRequestToManager=%v", rows.m_stmt.m_con.m_mapRequestToManager))
		}

		var fxpManager fastExportManager
		switch typedval := rows.m_stmt.m_con.m_mapRequestToManager [strings.TrimSpace (rows.m_sNativeSQL)].(type) {
			case fastExportManager:
				fxpManager = typedval.Value ()
			default:
				fxpManager = nil
		}

		if fxpManager == nil || fxpManager.hadWarningsAtNew () {
			if rows.m_stmt.m_con.hasDebugLog () {
				debugLog (fmt.Sprintf ("m_mapRequestToManager[%v]=%v", rows.m_sNativeSQL, fxpManager))
			}

			fxpManager, err = newFastExportManager (rows, ctx)
			if err != nil { return }

			fxpManager.setWarningsAtNew (len (fxpManager.getWarnings ()) > 0)
			rows.m_stmt.m_con.m_mapRequestToManager [strings.TrimSpace (rows.m_sNativeSQL)] = fxpManager
			if rows.m_sExportPathName != "" {
				fxpManager.setFileExportPath (rows.m_sExportPathName)
			}
		}

		// fxpManager is non-nil at this point

		if ! fxpManager.hadWarningsAtNew () {
			if ! fxpManager.isConnected () {

				delete (fxpManager.getAppCon ().m_mapRequestToErrors, rows.m_sNativeSQL) // clear errors
				fxpManager.setWarnings (make ([] string, 0)) // clear warnings

				if rows.m_stmt.m_con.hasDebugLog () {
					debugLog(fmt.Sprintf("m_mapRequestToManager[%v]=%v", rows.m_sNativeSQL, rows.m_stmt.m_con.m_mapRequestToManager [rows.m_sNativeSQL]))
				}

				rows.m_fastexportManager = fxpManager
				err = fxpManager.executeSelect (aBindValues, ctx)
				if err != nil { return }
			}

			rows = fxpManager.getControlRows ()
			rows.m_fastexportManager = fxpManager
			if rows.m_stmt.m_con.hasDebugLog () {
				debugLog (fmt.Sprintf ("m_fastexportManager=%v", rows.m_fastexportManager))
			}

			return
		}
	}

	if rows.m_stmt.m_con.hasDebugLog () {
		debugLog (fmt.Sprintf ("newTeradataRows: m_uRowsHandle=%v", rows.m_uRowsHandle))
	}

	var fldErrors error
	defer func () {
		err = chainErrors(fldErrors, err)
	} ()

	for _, manager := range rows.m_stmt.m_con.m_mapRequestToManager {
		switch typedval := manager.(type) {
			case fastLoadManager:
				fldManager := typedval.Value ()
				if rows.m_bCommitRequest && ! rows.m_bBypassTxCommands && ! fldManager.alreadyAutoCommitted () && ! fldManager.saveErrorsAndWarnings () {

					delete (fldManager.getAppCon ().m_mapRequestToErrors, rows.m_sNativeSQL) // clear errors
					fldManager.setWarnings (make ([] string, 0)) // clear warnings

					fldErrors = chainErrors(fldErrors, fldManager.endFastLoad(ctx))

				} else if strings.EqualFold (rows.m_sNativeSQL, "ABORT") || strings.EqualFold (rows.m_sNativeSQL, "ROLLBACK") {

					delete (fldManager.getAppCon ().m_mapRequestToErrors, rows.m_sNativeSQL) // clear errors
					fldManager.setWarnings (make ([] string, 0)) // clear warnings

					fldErrors = chainErrors(fldErrors, fldManager.rollbackFastLoad(ctx))
				}
		} // end switch
	} // end for

	if ! rows.m_bCommitRequest && ! rows.m_bBypassTxCommands && ! rows.m_stmt.m_con.isAnsiMode () && ! rows.m_stmt.m_con.m_bInTransaction {
		// when a rows object calls executeSessionCommand, specify teradata_bypass_tx_commands to avoid infinite recursion
		err = rows.m_stmt.m_con.executeSessionCommand (ctx, "{fn teradata_bypass_tx_commands}BT")
		if err != nil { return }
	}

	if rows.m_stmt.m_con.hasDebugLog () {
		debugLog (fmt.Sprintf ("newTeradataRows: m_uRowsHandle=%v", rows.m_uRowsHandle))
	}

	if rows.m_bCommitRequest && rows.m_sNativeSQL == "" && ! rows.m_bSendRunStartupParcel && ! rows.m_bSendOnly && ! rows.m_bReceiveOnly && ! bPartitionFastLoad && ! bPartitionFastExport {

		if rows.m_stmt.m_con.hasDebugLog () {
			debugLog (fmt.Sprintf ("newTeradataRows: m_bFreedSpool m_uRowsHandle=%v", rows.m_uRowsHandle))
		}

		rows.m_bFreedSpool = true

	} else if rows.isCreateProcedure () {

		if rows.m_stmt.m_con.hasDebugLog () {
			debugLog (fmt.Sprintf ("newTeradataRows: isCreateProcedure m_uRowsHandle=%v", rows.m_uRowsHandle))
		}

		err = rows.executeCreateProcedure (ctx)
		// do not return here upon error because commit must occur below

	} else if rows.m_fastexportManager == nil {

		if rows.m_stmt.m_con.hasDebugLog () {
			debugLog (fmt.Sprintf ("newTeradataRows: not m_fastexportManager m_uRowsHandle=%v", rows.m_uRowsHandle))
		}

		if len (aBindValues) > 0 || rows.m_fileImporter != nil {

			if bPartitionFastLoad && rows.m_stmt.m_con.m_fastloadBindMetadata != nil {
				aBindMetadata = rows.m_stmt.m_con.m_fastloadBindMetadata // FastLoad requires consistent bind metadata
				err = rows.executeSQLRequest (nil, aBindValues, aBindMetadata)
				// do not return here upon error because commit must occur below
			} else if bPartitionFastExport {

				bBatchInsert = func () (bool) { switch aBindValues [0].Value.(type) { case [][] any: return true ; default: return false } } ()
				if bBatchInsert {
					err = rows.m_stmt.m_con.makeDriverError (nil, 536, "Cannot FastExport batched metadata parameters")
				} else if len (aBindValues) != 3 {
					err = rows.m_stmt.m_con.makeDriverError (nil, 537, fmt.Sprintf ("FastExport expected metadata parameter count 3, got %v", len (aBindValues)))
				} else {
					var uaScale       [] uint8
					var uaRawDataType [] uint16
					var uaByteCount   [] uint64
					var sTypeName     string
					for i := 0 ; i < len (aBindValues) ; i++ {
						switch typedval := aBindValues [i].Value.(type) {
							case [] uint8:
								uaScale = typedval

							case [] uint16:
								uaRawDataType = typedval

							case [] uint64:
								uaByteCount = typedval

							case TeradataValue:
								_, _, _, _, sTypeName = dataTypeInfo (typedval.CookedDataType ())
								sTypeName = "(" + sTypeName + ")"
								err = rows.m_stmt.m_con.makeDriverError (nil, 538,
									fmt.Sprintf ("FastExport found unexpected metadata parameter %v type %T %v value %v",
										i + 1,
										typedval,
										sTypeName,
										typedval))

							default:
								err = rows.m_stmt.m_con.makeDriverError (nil, 538,
									fmt.Sprintf ("FastExport found unexpected metadata parameter %v type %T %v value %v",
										i + 1,
										typedval,
										sTypeName,
										typedval))
						}
					}
					if err == nil {
						if len (uaRawDataType) != len (uaByteCount) || len (uaRawDataType) != len (uaScale) {
							err = rows.m_stmt.m_con.makeDriverError (nil, 539,
								fmt.Sprintf ("FastExport expected equal lengths for metadata parameters, got len(uaScale)=%v, len(uaRawDataType)=%v, len(uaByteCount)=%v",
									len (uaScale),
									len (uaRawDataType),
									len (uaByteCount)))
						} else {
							rows.m_aColumnMetadata   = nil
							rows.m_asColumnNames     = nil
							rows.m_atColumnScanTypes = nil

							for i := range uaRawDataType {
								col := columnMetadata {}

								col.M_uRawDataType          = uaRawDataType [i]
								col.M_uByteCount            = uaByteCount [i]
								col.M_uScale                = uaScale [i]
								col.M_uCookedDataType       = cookedDataType (col.M_uRawDataType)
								_, _, _, _, col.M_sTypeName = dataTypeInfo (col.M_uCookedDataType)

								rows.m_aColumnMetadata   = append (rows.m_aColumnMetadata, col)
								rows.m_asColumnNames     = append (rows.m_asColumnNames, "")
								rows.m_atColumnScanTypes = append (rows.m_atColumnScanTypes, outboundType (rows.m_stmt.m_con.m_params.M_bTeradataValues, col.M_uCookedDataType))
							}

							aBindValues = make ([] driver.NamedValue, 0)
							err = rows.executeSQLRequest (nil, aBindValues, nil)
							// do not return here upon error because commit must occur below
						}
					}
				}
			} else if rows.m_fileImporter != nil {

				nStartRow := 1
				for {

					var executeErr error
					aaoRecords, nLastRecordProcessed, bParseError, readErr := rows.m_fileImporter.getBatchSegment (rows.m_stmt.m_con.m_params.M_uMaxMessageBodySize, false /*bFastloading*/)

					if rows.m_stmt.m_con.hasDebugLog () {
						debugLog (fmt.Sprintf ("Reading bind values (%v): maxSegmentSize=%v, nLastRecordProcessed=%v, startRow=%v, records returned=%v, readErr=%v",
							rows.m_fileImporter, rows.m_stmt.m_con.m_params.M_uMaxMessageBodySize, nLastRecordProcessed, nStartRow, len (aaoRecords), readErr))
					}

					// If an abort was sent but after the last request completed, no error was returned.
					// Stop & create an error only if we have verified there are more rows to be processed.
					if rows.m_bAbortSent && len (aaoRecords) > 0 {

						ctxErr := rows.m_stmt.m_con.makeInterruptError (ctx.Err (), true /*abort sent*/)
						err = rows.m_stmt.m_con.makeDriverError (ctxErr, 528, fmt.Sprintf ("A failure occurred while executing row %v of a batch request and the remaining batch request failed.", nStartRow))
						break
					}

					// We need to end our insert, but make sure previous errors are returned
					if readErr != nil && ! bParseError {
						err = chainErrors (err, rows.m_stmt.m_con.makeDriverError (err, 541, fmt.Sprintf ("Read error: %v", readErr)))
						break
					}

					if len (aaoRecords) > 0 {

						aNamedBatchValues := []driver.NamedValue{driver.NamedValue{Ordinal: 1, Value: aaoRecords}}

						var bindErr error
						if rows.m_sCSVPathName != "" {
							aBindMetadata, bindErr = rows.validateCSVFileValues (aNamedBatchValues)
						} else if rows.m_sParquetPathName != "" {
							aBindMetadata, bindErr = rows.validateParquetFileValues (aNamedBatchValues)
						}

						if bindErr != nil {
							err = chainErrors (err, bindErr)
							break
						}

						executeErr = rows.executeSQLRequest (ctx, aNamedBatchValues, aBindMetadata)

					} else if len (aaoRecords) == 0 && readErr == nil { break }

					if executeErr != nil || readErr != nil {

						// We may have both read and execute error so we combine executeErr & readErr
						errChain := chainErrors (executeErr, readErr)

						// In ANSI mode, an asynchronous abort causes full transaction rollback. If auto commit is on in ANSI mode, the commit is sent
						// at the end of the entire batch if no error occurs. In TERA mode any error causes full transaction rollback (auto commit off).
						if executeErr != nil && (
							wasInterrupted (ctx) && rows.m_bAbortSent && rows.m_stmt.m_con.isAnsiMode () ||
							! rows.m_stmt.m_con.isAnsiMode () && ! rows.m_bCommitRequest) {

							executeErr = rows.m_stmt.m_con.makeDriverError (errChain, 528, fmt.Sprintf ("A failure occurred while executing rows %v through %v and the entire batch request failed.", nStartRow, nLastRecordProcessed))
							err = chainErrors (executeErr, err)
							break
						}

						// If a segment was interrupted before it was sent to the database or the segment was aborted in TERA mode (auto commit on),
						// stop processing any further segments. Any previous segments may still succeed.
						if wasInterrupted (ctx) || rows.m_bAbortSent {

							executeErr = rows.m_stmt.m_con.makeDriverError (errChain, 528, fmt.Sprintf ("A failure occurred while executing rows %v through %v and the remaining rows in the batch request failed", nStartRow, nLastRecordProcessed))
							err = chainErrors (executeErr, err)
							break
						}

						if executeErr == nil {
							executeErr = rows.m_stmt.m_con.makeDriverError (errChain, 528, fmt.Sprintf ("A failure occurred while reading rows %v through %v from a batch file and one or more of these rows were not inserted. Details of the failures can be found in this error message.", nStartRow, nLastRecordProcessed))
						} else {
							executeErr = rows.m_stmt.m_con.makeDriverError (errChain, 528, fmt.Sprintf ("A failure occurred while executing rows %v through %v of a batch request and none of these rows were inserted.", nStartRow, nLastRecordProcessed))
						}

						err = chainErrors (err, executeErr)
						// do not return here upon error because commit must occur below
					}

					nStartRow = nLastRecordProcessed + 1

				} // end of reading segments from file
			} else {

				if aBindMetadata == nil {
					bBatchInsert = func ()(bool) { switch aBindValues[0].Value.(type) { case [][] any: return true ; default: return false } } ()
				}

				if bBatchInsert {

					if rows.m_stmt.m_con.hasDebugLog () {
						debugLog (fmt.Sprintf ("newTeradataRows: bBatchInsert m_uRowsHandle=%v", rows.m_uRowsHandle))
					}

					rows.m_bProcessingBatchRequest = true

					// validateBatchValues needs to be called before the segment sizes are calculated
					// since validateBatchValues may significantly adjust the length of the values.
					if aBindMetadata == nil {
						aBindMetadata, err = rows.validateBatchValues (aBindValues)
					}
					if err == nil {

						aaBatchValues := aBindValues [0].Value.([][] driver.NamedValue)
						anRowIndex := rows.calculateBatchSegmentSizes (aaBatchValues)
						if rows.m_batchStatementErrorMap == nil {
							rows.m_batchStatementErrorMap = make (map [uint16] batchStatementError)
						}

						for i := 0 ; i < len (anRowIndex) - 1 ; i++ {

							rows.m_uBatchSegmentStartRow = uint16 (anRowIndex [i])

							executeErr := rows.executeSQLRequest (ctx, [] driver.NamedValue {driver.NamedValue { Ordinal : 1, Value : aaBatchValues [anRowIndex [i] : anRowIndex [i + 1]]}}, aBindMetadata)

							// If an abort was sent but there is no error, the abort was sent too late. The statements in that segement were
							// successful. However, if there are more segments left, create an error and stop processing the batch.
							if executeErr == nil && rows.m_bAbortSent && i < len (anRowIndex) - 2 {

								ctxErr := rows.m_stmt.m_con.makeInterruptError (ctx.Err (), true /*abort sent*/)

								nFirstStmt := func () int { if rows.m_bCommitRequest { return anRowIndex [i + 1] + 1} ; return 1 } ()
								err = rows.generateBatchErrors (nFirstStmt, len (aaBatchValues), ctxErr)

								break
							}

							if executeErr != nil {

								if rows.m_stmt.m_con.hasDebugLog () {
									debugLog (fmt.Sprintf ("Batch request failed while executing segment %v of %v before row %v (m_bTryFastLoad = %v, isAnsiMode = %v, m_bCommitRequest = %v, wasInterrupted = %v & m_bAbortSent = %v).",
										i + 1,
										len (anRowIndex) - 1,
										anRowIndex [i] + 1,
										rows.m_bTryFastLoad,
										rows.m_stmt.m_con.isAnsiMode (),
										rows.m_bCommitRequest,
										wasInterrupted (ctx),
										rows.m_bAbortSent))
								}

								// In ANSI mode, an asynchronous abort causes full transaction rollback. If auto commit is on in ANSI mode, the commit is sent
								// at the end of the entire batch if no error occurs. In TERA mode any error causes full transaction rollback (auto commit off).
								if (wasInterrupted (ctx) && rows.m_bAbortSent && rows.m_stmt.m_con.isAnsiMode ()) || (! rows.m_stmt.m_con.isAnsiMode () && ! rows.m_bCommitRequest) {

									err = rows.generateBatchErrors (1, len (aaBatchValues), executeErr)
									break // do not return here upon error because commit must occur below
								}

								// If a segment was interrupted before it was sent to the database or the segment was aborted,
								// stop processing any further segments. Any previous segments may still succeed.
								if wasInterrupted (ctx) || rows.m_bAbortSent {

									if rows.m_stmt.m_con.hasDebugLog () {
										debugLog (fmt.Sprintf ("Interrupted while processing batch segment starting at row %v", anRowIndex [i] + 1))
									}

									err = rows.generateBatchErrors (anRowIndex [i] + 1, len (aaBatchValues), executeErr)
									break
								}

								// An error occurred but continue processing the request
								err = rows.generateBatchErrors (anRowIndex [i] + 1, anRowIndex [i + 1], executeErr)
							}
						} // end for

						var asBatchErrorMsgs [] string

						// Create database error or driver error for each error reported in the batch and chain them together
						for iStmt := 1 ; iStmt <= len (aaBatchValues) ; iStmt ++ {

							if stmtError, ok := rows.m_batchStatementErrorMap [uint16 (iStmt)] ; ok {

								var sBatchError string
								switch stmtError.m_uErrorCode {

									case 529: sBatchError = rows.m_stmt.m_con.makeDriverError (nil, 529,
										fmt.Sprintf ("Batch request failed to execute %v of %v batched statements. Batched statement %v failed to execute because of Teradata SQL Driver error %v, %v",
											len (rows.m_batchStatementErrorMap),
											len (aaBatchValues),
											iStmt,
											stmtError.m_uErrorCode,
											stmtError.m_sErrorMsg)).Error ()

									default: sBatchError = rows.m_stmt.m_con.formatDatabaseError (nil, stmtError.m_uErrorCode,
										fmt.Sprintf ("Batch request failed to execute %v of %v batched statements. Batched statement %v failed to execute because of Teradata Database error %v, %v",
											len (rows.m_batchStatementErrorMap),
											len (aaBatchValues),
											iStmt,
											stmtError.m_uErrorCode,
											stmtError.m_sErrorMsg)).Error ()
								}

								asBatchErrorMsgs = append (asBatchErrorMsgs, sBatchError)
							}
						} // end for iStmt

						rows.m_stmt.m_con.m_mapRequestToErrors [rows.m_sNativeSQL] = asBatchErrorMsgs

					} // end if err == nil

				} else { // not bBatchInsert

					if rows.m_stmt.m_con.hasDebugLog () {
						debugLog (fmt.Sprintf ("newTeradataRows: not bBatchInsert m_uRowsHandle=%v", rows.m_uRowsHandle))
					}

					if aBindMetadata == nil {
						aBindMetadata, err = rows.validateBindValues (aBindValues)
					}

					if err == nil {
						err = rows.executeSQLRequest (ctx, aBindValues, aBindMetadata)
						// do not return here upon error because commit must occur below
					}
				} // end else not bBatchInsert
			}

		} else {

			if rows.m_stmt.m_con.hasDebugLog () {
				debugLog (fmt.Sprintf ("newTeradataRows: m_uRowsHandle=%v", rows.m_uRowsHandle))
			}

			err = rows.executeSQLRequest (ctx, aBindValues, nil)
			// do not return here upon error because commit must occur below
		} // end if aBindValues
	}

	if rows.m_stmt.m_con.hasDebugLog () {
		debugLog (fmt.Sprintf ("newTeradataRows: m_uRowsHandle=%v", rows.m_uRowsHandle))
	}

	if rows.m_bCommitRequest && ! rows.m_bBypassTxCommands {
		// when a rows object calls executeSessionCommand, specify teradata_bypass_tx_commands to avoid infinte recursion
		sCommit := "{fn teradata_bypass_tx_commands}" +
			func () string { if rows.m_stmt.m_con.isAnsiMode () { return "COMMIT" } ; return "ET" } ()

		for rows.m_stmt.m_con.m_bInTransaction {

			errSecond := rows.m_stmt.m_con.executeSessionCommand (ctx, sCommit)
			if errSecond != nil {
				err = chainErrors (err, errSecond)
				return
			}
		} // end for
	} // end if

	return

} // end func newTeradataRows

func (rows *TeradataRows) composeOAuthTokensParcelBody (aasScopeTokenPairs [][] string) (abyParcelBody [] byte) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter composeOAuthTokensParcelBody %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave composeOAuthTokensParcelBody %v len=%v", rows, len (abyParcelBody))) } ()
	}

	abyParcelBody = fourBytes (uint32 (len (aasScopeTokenPairs))) // 4-byte integer count of subsequent pairs of VARCHAR scope and token values

	for _, asScopeAndToken := range aasScopeTokenPairs {
		for _, sScopeOrToken := range asScopeAndToken {
			abyParcelBody = append (abyParcelBody, rows.m_stmt.m_con.serializeVarchar (sScopeOrToken) ...) // 2-byte length prefix + scope or token value encoded in session character set
		}
	}

	return

} // end composeOAuthTokensParcelBody

func (rows *TeradataRows) webService (sMethod string, sURL string, sRequestBody string) (sResponse string, err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter webService %v sMethod=%v sURL=%v", rows, sMethod, sURL))
		defer func () { traceLog (fmt.Sprintf ("< leave webService %v sMethod=%v sURL=%v err=%v", rows, sMethod, sURL, err)) } ()
	}

	con    := rows.m_stmt.m_con
	params := con.m_params

	if ! params.M_bTokenMode {
		err = con.makeDriverError (nil, 0, "teradata_web_service is only available in token mode")
		return
	}

	if ! strings.HasPrefix (sURL, "https://") {
		sURL = makePath (params.M_sWsEndpoint, "/", sURL)
	}

	aasHeaders := [][] string {
		{"Content-Type", "application/json"},
		{"Accept", "application/json"},
	}

	asFinders := con.m_setTokenFinders.ToSlice ()
	if len (asFinders) > 0 {

		var asPair [] string
		asPair, err = con.findToken (asFinders [0])
		if err != nil { return }

		sToken := asPair [1] // token is second element of pair
		aasHeaders = append (aasHeaders, [] string {"Authorization", "Bearer " + sToken})
	}

	if strings.HasPrefix (sRequestBody, "file:") {
		sFilePathName := strings.TrimPrefix (sRequestBody, "file:")

		var abyFileContent [] byte
		abyFileContent, err = os.ReadFile (sFilePathName)
		if err != nil {
			err = con.makeDriverError (err, 0, fmt.Sprintf ("teradata_web_service cannot read request body file %v", sFilePathName))
			return
		}

		sRequestBody = string (abyFileContent)

	} else if strings.HasPrefix (sRequestBody, "base64:") {
		sBase64Content := strings.TrimPrefix (sRequestBody, "base64:")

		var abyDecodedContent [] byte
		abyDecodedContent, err = smartDecodeBase64 (sBase64Content)
		if err != nil {
			err = con.makeDriverError (err, 0, fmt.Sprintf ("teradata_web_service cannot decode base64 request body %v", sBase64Content))
			return
		}

		sRequestBody = string (abyDecodedContent)

	} else if sRequestBody != "" {
		err = con.makeDriverError (nil, 0, "teradata_web_service request body must start with file: or base64:")
		return
	}

	resp, err := DoHttpRequest (params, sMethod, sURL, aasHeaders, [] byte (sRequestBody))
	if err != nil { return }

	sResponse = fmt.Sprintf (`{"code":%v,"message":%v,"body":%v}`,
		resp.M_nCode,
		rows.safeMarshal (resp.M_sMessage),
		coalesce (strings.TrimSpace (string (resp.M_abyBody)), "null"))

	return

} // end webService

func (rows *TeradataRows) provideToken () (sToken string, err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter provideToken %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave provideToken %v token=%v err=%v", rows, sToken, err)) } ()
	}

	con    := rows.m_stmt.m_con
	params := con.m_params

	if ! params.M_bTokenMode {
		err = con.makeDriverError (nil, 0, "teradata_provide(token) is only available in token mode")
		return
	}

	asFinders := con.m_setTokenFinders.ToSlice ()
	if len (asFinders) > 0 {

		var asPair [] string
		asPair, err = con.findToken (asFinders [0])
		if err != nil { return }

		sToken = asPair [1] // token is second element of pair
	}

	return

} // end provideToken

func (rows *TeradataRows) initControlData (uMessageKind byte, uRequestNumber uint32, aabyParcels ... [] byte) (abyOutput [] byte, err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter initControlData %v %v", rows, MessageKind (uMessageKind)))
		defer func () { traceLog (fmt.Sprintf ("< leave initControlData %v req=%v %v len=%v err=%v", rows, uRequestNumber, MessageKind (uMessageKind), len (abyOutput), err)) } ()
	}

	con    := rows.m_stmt.m_con
	params := con.m_params

	aasScopeTokenPairs := [][] string {}

	for _, sFinder := range con.m_setTokenFinders.ToSlice () {

		var asPair [] string
		asPair, err = con.findToken (sFinder)

		// initControlData only returns a non-nil error for an OIDC/OAuth token refresh failure, and the caller must choose the appropriate action.
		// Raise this error to the application for a Start Request message, but ignore and debug log for a Continue Request message.
		if err != nil { return }

		if asPair != nil {
			aasScopeTokenPairs = append (aasScopeTokenPairs, asPair)
		}
	}

	bWantToSendControlData := con.isRNPActive () || len (aasScopeTokenPairs) > 0
	bCanSendControlData    := params.M_uRedrive >= 1 && con.databaseCapableOfRedriveLevel () >= 1

	if con.hasDebugLog () {
		debugLog ("initControlData:" +
			" m_setTokenFinders.Len=" + itoa (con.m_setTokenFinders.Len ()) +
			" len(aasScopeTokenPairs)=" + itoa (len (aasScopeTokenPairs)) +
			" isRNPActive=" + btoa (con.isRNPActive ()) +
			" M_uRedrive=" + utoa (params.M_uRedrive) +
			" databaseCapableOfRedriveLevel=" + utoa (con.databaseCapableOfRedriveLevel ()) +
			" bWantToSendControlData=" + btoa (bWantToSendControlData) +
			" bCanSendControlData=" + btoa (bCanSendControlData))
	}

	uControlDataLength := uint32 (0)

	if bWantToSendControlData && bCanSendControlData {

		aabyControlDataParcels := [][] byte {}

		abyStart := makeParcel (PCLCONTROLDATASTART, fourBytes (0)) // set actual length below
		aabyControlDataParcels = append (aabyControlDataParcels, abyStart)

		if con.isRNPActive () {
			aabyControlDataParcels = append (aabyControlDataParcels, makeParcel (PCLRECOVERABLEPROTOCOL))
		}

		if con.isRedriveActive () {
			aabyControlDataParcels = append (aabyControlDataParcels, makeParcel (PCLREDRIVE, [] byte { 1 })) // enabled
		}

		if rows.m_abyContinueContextBody != nil {
			aabyControlDataParcels = append (aabyControlDataParcels, makeParcel (PCLCONTINUECONTEXT, rows.m_abyContinueContextBody))
		}

		if len (aasScopeTokenPairs) > 0 {
			aabyControlDataParcels = append (aabyControlDataParcels, makeParcel (PCLOAUTHTOKENS, rows.composeOAuthTokensParcelBody (aasScopeTokenPairs)))
		}

		abyEnd := makeParcel (PCLCONTROLDATAEND, fourBytes (0)) // set actual length below
		aabyControlDataParcels = append (aabyControlDataParcels, abyEnd)

		uControlDataLength = uint32 (combinedLength (aabyControlDataParcels))
		binary.BigEndian.PutUint32 (constrainedParcelBody (abyStart), uControlDataLength)
		binary.BigEndian.PutUint32 (constrainedParcelBody (abyEnd  ), uControlDataLength)

		aabyParcels = append (aabyParcels, aabyControlDataParcels ...)
	}

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("initControlData: uControlDataLength=%v", uControlDataLength))
	}

	abyOutput = con.makeRequestMessage (uMessageKind, NEW_CRYPTO_NONCE, uRequestNumber, aabyParcels ...)
	binary.BigEndian.PutUint32 (abyOutput [40 : ], uControlDataLength)
	return

} // end method initControlData

func (rows *TeradataRows) executeSQLRequest (ctx context.Context, aBindValues [] driver.NamedValue, aBindMetadata [] bindMetadata) (err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter executeSQLRequest %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave executeSQLRequest %v", rows)) } ()
	}

	var aabyRequestParcels [][] byte

	bPartitionDBCCONS    := strings.EqualFold (rows.m_stmt.m_con.m_params.M_sPartition, "DBCCONS")
	bPartitionMonitor    := strings.EqualFold (rows.m_stmt.m_con.m_params.M_sPartition, "MONITOR")
	bPartitionFastLoad   := strings.EqualFold (rows.m_stmt.m_con.m_params.M_sPartition, "FASTLOAD")
	bPartitionFastExport := strings.EqualFold (rows.m_stmt.m_con.m_params.M_sPartition, "EXPORT")

	if rows.m_stmt.m_con.hasDebugLog () {
		debugLog (fmt.Sprintf ("executeSQLRequest: bPartitionDBCCONS=%v bPartitionMonitor=%v bPartitionFastLoad=%v bPartitionFastExport=%v m_sNativeSQL=%v (len=%v)",
			bPartitionDBCCONS, bPartitionMonitor, bPartitionFastLoad, bPartitionFastExport, rows.m_sNativeSQL, len (rows.m_sNativeSQL)))
	}

	if bPartitionDBCCONS {

		if rows.m_stmt.m_con.m_bPromptMode {
			aabyRequestParcels = append (aabyRequestParcels, makeParcel (PCLREQUEST, [] byte (" ")), makeParcel (PCLDATA, twoBytes (uint16 (len (rows.m_sNativeSQL))), [] byte (rows.m_sNativeSQL)))
		} else {
			aabyRequestParcels = append (aabyRequestParcels, makeParcel (PCLREQUEST, [] byte (rows.m_sNativeSQL)))
		}

	} else if bPartitionFastLoad {

		// do nothing

	} else if bPartitionFastExport {

		aabyRequestParcels = append (aabyRequestParcels, makeParcel (PCLREQUEST, [] byte (rows.m_sNativeSQL))) // makeParcel automatically composes alt-header if needed

	} else { // Monitor or SQL

		if ! bPartitionMonitor {
			aabyRequestParcels = append (aabyRequestParcels, makeParcel (PCLOPTIONS, rows.composeOptionsParcelBody ()))
		}

		if rows.m_bSendRunStartupParcel {
			aabyRequestParcels = append (aabyRequestParcels, makeParcel (PCLRUNSTARTUP))
		} else {
			aabyRequestParcels = append (aabyRequestParcels, makeParcel (PCLINDICREQ, [] byte (rows.m_sNativeSQL))) // makeParcel automatically composes alt-header if needed
		}
	} // end else Monitor or SQL

	if len (aBindValues) > 0 {

		if ! bPartitionFastLoad && ! bPartitionMonitor {
			if rows.m_bSendSIP {
				aabyRequestParcels = append (aabyRequestParcels, rows.composeStatementInfoParcel (aBindMetadata), makeParcel (PCLSTATEMENTINFOEND))
			} else {
				aabyRequestParcels = append (aabyRequestParcels, rows.composeDataInfoParcel (aBindMetadata))
			}
		}

		switch aBindValues [0].Value.(type) {

			case [][] driver.NamedValue:

				rows.m_stmt.m_con.m_nFastLoadCurrentRow = 0
				uRequestParcelLength := rows.m_uRequestLimit

				var uRespParcelLength uint32
				if bPartitionFastLoad {
					uRespParcelLength = uint32 (len (makeParcel (PCLRESP, twoBytes (65104))))
				}

				// Compose a data parcel for every row of bound values
				aaBatchValues := aBindValues [0].Value.([][] driver.NamedValue)
				for _, aBoundValues := range aaBatchValues {
					abyDataParcel := rows.composeDataParcel (aBoundValues)
					if bPartitionFastLoad {
						if uint32(len(abyDataParcel)) > uRequestParcelLength - uRespParcelLength {
							rows.m_stmt.m_con.m_nFastLoadCurrentRow = len(aabyRequestParcels)
							break
						} else {
							uRequestParcelLength -= uint32(len(abyDataParcel))
						}
					}

					aabyRequestParcels = append (aabyRequestParcels, abyDataParcel)

					if rows.m_bUseLOB {
						aabyRequestParcels = append (aabyRequestParcels, makeParcel (PCLENDMULTIPARTINDICDATA))
					}
				} // end for

			default:

				aabyRequestParcels = append (aabyRequestParcels, rows.composeDataParcel (aBindValues))

				if rows.m_bUseLOB {
					aabyRequestParcels = append (aabyRequestParcels, makeParcel (PCLENDMULTIPARTINDICDATA))
				}
		} // end switch
	} // end if aBindValues

	aabyRequestParcels = append (aabyRequestParcels, func () [] byte {
		if bPartitionDBCCONS || bPartitionFastLoad {
			return makeParcel (PCLRESP, twoBytes (65104))
		} else {
			return makeParcel (func () uint16 { if rows.keepResponse () { return PCLBIGKEEPRESP } ; return PCLBIGRESP } (),
				/*Field MaxResponseMessageBodySize*/ fourBytes (rows.m_uResponseLimit))
		}
	} () )

	var abyStartRequestMsg [] byte
	if ! rows.m_bReceiveOnly {
		abyStartRequestMsg, err = rows.initControlData (MESSAGE_KIND_START, NEW_REQUEST_NUMBER, aabyRequestParcels ...)
		if err != nil { return } // fail for OIDC/OAuth token refresh failure
	}

	if rows.m_bSendOnly {
		err = rows.m_stmt.m_con.sendMessage (&NetworkIO { abyMsgToSend: abyStartRequestMsg })
		return
	}

	var abyStartResponseMsg [] byte
	if rows.m_bReceiveOnly {
		abyStartResponseMsg, _, err = rows.m_stmt.m_con.receiveMessage (&NetworkIO {}, /*errSend*/ nil)
	} else if rows.m_stmt.m_con.m_params.M_uConnectFunction == 0 {
		abyStartResponseMsg, rows.m_bAbortSent, err = rows.m_stmt.m_con.sendAndReceive (&NetworkIO { ctx: ctx, abyMsgToSend: abyStartRequestMsg, bAbortable: true, rows: rows })
	} else {
		abyStartResponseMsg, _, err = rows.m_stmt.m_con.sendAndReceive (&NetworkIO { abyMsgToSend: abyStartRequestMsg, rows: rows })
	}
	if err != nil { return }

	rows.m_uRequestNumber = binary.BigEndian.Uint32 (abyStartResponseMsg [32 : ])

	aabyParcelHeaders := getParcelHeadersFromMessage (abyStartResponseMsg)

	if rows.m_stmt.m_con.hasDebugLog () { debugLogParcelHeaders ("newTeradataRows", aabyParcelHeaders) }

	var errFile error
	for peekCookedParcelFlavor (0, aabyParcelHeaders) == PCLELICITFILE {

		aabyParcelHeaders, errFile, err = rows.processElicitFileParcel (ctx, aabyParcelHeaders [0])
		if err != nil { return }
	}

	// Process deferred LOBs and continue to do so until all of the LOBs are sent
	for peekCookedParcelFlavor (0, aabyParcelHeaders) == PCLELICITDATA {

		aabyParcelHeaders, errFile, err = rows.processElicitDataParcel (ctx, aabyParcelHeaders [0])
		if err != nil { return }
	}

	rows.processEndRequest (aabyParcelHeaders)

	err = rows.processResponseBundle (errFile, aabyParcelHeaders) // may call fetchNextParcel
	if err != nil { return }

	err = rows.fetchMessagesFromResultSet ()
	if err != nil { return }

	if warningIsReallyError (rows.m_uWarningCode) {

		var errCause error // initialized to nil
		if rows.m_sWarningMsg != "" { errCause = errors.New (rows.m_sWarningMsg) } // contains UDF compilation errors

		err = rows.m_stmt.m_con.formatDatabaseError (errCause, rows.m_uWarningCode, rows.m_sRawWarningMsg)

		if rows.m_stmt.m_con.hasDebugLog () { debugLog (fmt.Sprintf ("warningIsReallyError: %v %v", rows.m_stmt.m_con, err)) }

		return
	}

	if rows.m_sExportPathName != "" {

		if bPartitionFastExport {
			err = rows.exportResultSet (bPartitionFastExport, 0)
			return
		}

		nResultSet := 0
		for { // more result sets

			if resultSetExpected (rows.m_uActivityType) || (rows.m_bFakeResultSets && rows.m_nFakeState == returnRow) {
				nResultSet ++
			}

			err = rows.exportResultSet (bPartitionFastExport, nResultSet)
			if err != nil { return }

			err = rows.NextResultSet ()
			if errors.Is (err, io.EOF) {
				err = nil
				break
			}

			if err != nil { return }

		} // end for result sets
	} // end if m_sExportPathName

	return

} // end method executeSQLRequest

func (rows *TeradataRows) generateBatchErrors (nStartStmt int, nEndStmt int, baseError error) (err error) {

	// Based on code generate errors for any statements not sent. Let any statements sent be committed if auto commit off
	if ! rows.m_bTryFastLoad {

		err = rows.m_stmt.m_con.makeDriverError (baseError, 528,
			fmt.Sprintf ("A failure occurred while executing rows %v through %v of a batch request. Details of the failure can be found in the error string that is accessible using the escape functions {fn teradata_nativesql}{fn teradata_get_errors}.",
				nStartStmt,
				nEndStmt))

		// do not return here upon error because commit must occur below
	}

	for iStmt := nStartStmt ; iStmt <= nEndStmt ; iStmt ++ {

		// Anything that has been sent so far will be inserted, but anything remaining in the batch will not be submitted and will fail
		if _, ok := rows.m_batchStatementErrorMap [uint16 (iStmt)] ; ! ok {
			rows.m_batchStatementErrorMap [uint16 (iStmt)] = batchStatementError {529, fmt.Sprintf ("An failure occurred while executing a batch request. The statement %v was not executed.", iStmt)}
		}
	}

	return

} // end method generateBatchErrors

func (rows *TeradataRows) exportResultSet (bPartitionFastExport bool, nResultSet int) (err error) {

	if ! resultSetExpected (rows.m_uActivityType) && ! bPartitionFastExport {

		// If there is a statement that doesn't return a result set, add a warning only if the fake
		// result set escape syntax was specified. This is the only way to access the warning.

		if ! rows.m_bFakeResultSets || rows.m_nFakeState != returnRow { return }

		rows.m_uWarningCode = 547
		rows.m_sWarningMsg = rows.m_stmt.m_con.makeDriverWarning (rows.m_uWarningCode,
			fmt.Sprintf ("No result set was produced for statement %v (activity type '%v'). Cannot export results to file %v.",
				rows.m_uStatementNumber,
				activityType (rows.m_uActivityType),
				rows.m_sExportPathName))
	}

	asColumnNames := rows.Columns ()
	if rows.m_fileExporter == nil {

		sExportPathName := rows.getExportFilePathName (rows.m_sExportPathName, nResultSet - 1)
		rows.m_fileExporter, err = rows.createFileExporter (sExportPathName, asColumnNames, rows.m_sFieldSeparator, rows.m_sFieldQuote)

		defer func () {
			if rows.m_fileExporter != nil {
				rows.m_fileExporter.close ()
				rows.m_fileExporter = nil
			}
		} ()

		if err != nil { return }
	}

	dest := make ([] driver.Value, len (asColumnNames))

	for nRow := 1 ; ; nRow++ {

		err = rows.Next (dest)
		if err != nil  {
			if errors.Is (err, io.EOF) { err = nil }
			break
		}
		err = rows.m_fileExporter.exportRecord (dest, nRow, false /* bFastExporting */)
		if err != nil { return }
	}

	return

} // end method exportResultSet

func (rows *TeradataRows) composeOptionsParcelBody () (abyOptionsParcelBody [] byte) {

	bDynamicRSAvailable   := rows.m_stmt.m_con.m_config.M_uSPDynamicResultSetsSupport >= 1
	bEONReturnAvailable   := rows.m_stmt.m_con.m_config.M_uExtendedObjectNamesInParcels >= 2
	bStmtIndepAvailable   := rows.m_stmt.m_con.m_config.M_uStatementIndependenceSupport >= 1
	bFailFastAvailable    := rows.m_stmt.m_con.m_config.M_uTDWMFailFastSupport >= 1
	bBigRowsAvailable     := rows.m_stmt.m_con.m_config.M_uMaxAvailableBytesInResponseRow > 64768
	bVSDAvailable         := rows.m_stmt.m_con.m_config.M_uVSDSupport >= 1

	abyOptionsParcelBody = [] byte {
		/*  1 RequestMode             */ func () byte { if rows.m_bUseLOB { return /*Multipart*/ 'M' } ; return /*Indic*/ 'I' } (),
		/*  2 RequestProcessingOption */ rows.m_byRPO,
		/*  3 LOBSelect               */ func () byte { if rows.m_bUseLOB { return rows.m_byLOBSelect } ; return 0 } (),
		/*  4 CLOBTranslate           */ func () byte { if rows.m_bUseLOB { return rows.m_byCLOBTranslate } ; return 0 } (),
		/*  5 APHResponses            */ 'Y',
		/*  6 ReturnSIP               */ func () byte { if rows.m_bWantSIP { return 'Y' } ; return 0 } (),
		/*  7 UDTTransformsOff        */ func () byte { if rows.m_bUseUDTTransforms { return 0 } ; return 'Y' } (), // reverse logic
		/*  8 MaxDecimalPrecision     */ 38,
		/*  9 AutoGeneratedKeys       */ rows.m_byAGKR,
		/* 10 ReturnDynamicResultSets */ func () byte { if bDynamicRSAvailable { return 'Y' } ; return 0 } (),
		/* 11 SPReturnResult          */  0,
		/* 12 ReturnPeriodsAsStructs  */  0,
		/* 13 ReturnExtObjectNames    */ func () byte { if bEONReturnAvailable { return 1 } ; return 0 } (),
		/* 14 TrustMode               */  0,
		/* 15 StatementIndependence   */ func () byte { if bStmtIndepAvailable && rows.m_bProcessingBatchRequest { return 'Y' } ; return 0 } (),
		/* 16 ArrayTransformOff       */ func () byte { if rows.m_bUseArrayTransform { return 0 } ; return 'Y' } (), // reverse logic
		/* 17 XMLDataFormat           */  0,
		/* 18 FailFast                */ func () byte { if bFailFastAvailable && rows.m_bFailFast { return 'Y' } ; return 0 } (),
		/* 19 UnityTDWM               */  0,
		/* 20 SendSPSQLToUnity        */  0,
		/* 21 1MB Response Rows       */ func () byte { if bBigRowsAvailable && rows.m_bWantBigRows { return 1 } ; return 0 } (),
		/* 22 IncludeCatalogName      */ func () byte { if bVSDAvailable { return 'Y' } ; return 'N' } (),
	}

	if bVSDAvailable {

		abyOptionsParcelBody = abyOptionsParcelBody [ : 22]

	} else if bBigRowsAvailable {

		abyOptionsParcelBody = abyOptionsParcelBody [ : 21]

	} else if rows.m_stmt.m_con.m_config.M_uXMLDataTypeAndXQuerySupport >= 1 ||
		bFailFastAvailable {

		abyOptionsParcelBody = abyOptionsParcelBody [ : 18]

	} else if rows.m_stmt.m_con.m_config.M_uArrayDataTypeSupport >= 1 {

		abyOptionsParcelBody = abyOptionsParcelBody [ : 16]

	} else if bEONReturnAvailable ||
		rows.m_stmt.m_con.m_config.M_uStructSupport >= 2 || // Period-as-Struct support
		rows.m_stmt.m_con.m_config.M_uTrustedSQLRequestSupport >= 1 ||
		bStmtIndepAvailable {

		abyOptionsParcelBody = abyOptionsParcelBody [ : 15] // TD 13.10 requires 15 bytes

	} else if bDynamicRSAvailable {

		abyOptionsParcelBody = abyOptionsParcelBody [ : 11]

	} else {

		abyOptionsParcelBody = abyOptionsParcelBody [ : 10]
	}

	return

} // end composeOptionsParcelBody

func (rows *TeradataRows) calculateBatchSegmentSizes (aaBatchValues [][] driver.NamedValue) [] int {

	nSegmentSize := uint32 (0)
	anRowIndex := [] int {0}

	for nRow, aBindValues := range aaBatchValues {

		nRowSize := uint32 (0)
		for _, namedval := range aBindValues {
			nRowSize += uint32 (namedval.Value.(TeradataValue).ByteCount ())
		}

		if nRow > 0 && nSegmentSize + nRowSize > rows.m_stmt.m_con.m_params.M_uMaxMessageBodySize {
			anRowIndex = append (anRowIndex, nRow)
			nSegmentSize = nRowSize

		} else if nRow != len (aaBatchValues) && nRow % 16383 == 16382 {
			anRowIndex = append (anRowIndex, nRow + 1)
			nSegmentSize = 0

		} else { nSegmentSize += nRowSize }

	}
	if anRowIndex[len(anRowIndex)-1] != len (aaBatchValues) {
		anRowIndex = append (anRowIndex, (len (aaBatchValues)))
	}

	return anRowIndex
}  // end calculateBatchSegmentSizes

func (rows *TeradataRows) validateBindValues (aBindValues [] driver.NamedValue) (aBindMetadata [] bindMetadata, err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter validateBindValues %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave validateBindValues %v", rows)) } ()
	}

	if len (aBindValues) > 0xFFFF {
		err = rows.m_stmt.m_con.makeDriverError (nil, 0, fmt.Sprintf ("Too many bound parameter values: %v", len (aBindValues)))
		return
	}

	aBindMetadata = make ([] bindMetadata, len (aBindValues))

	for nParameter, escapeParamTypeMetadata := range rows.m_mapParameterDataTypes {

		if (nParameter > len (aBindValues)) {

			err = rows.m_stmt.m_con.makeDriverError (nil, 0,
				fmt.Sprintf ("Parameter number %v specified in the teradata_parameter escape function exceeds the parameter count %v",
					nParameter,
					len (aBindValues)))

			return

		}

		err = rows.validateMappedValue (nParameter, escapeParamTypeMetadata.m_uCookedDataType, aBindValues [nParameter - 1].Value)
		if err != nil { return }

		aBindValues [nParameter - 1].Value = rows.convertMappedValue (escapeParamTypeMetadata, &aBindMetadata [nParameter - 1], aBindValues [nParameter - 1].Value)
	}

	for iParam, namedval := range aBindValues {

		if rows.m_stmt.m_con.hasDebugLog () {
			debugLog (fmt.Sprintf ("validateBindValues: iParam=%v TypeOf=%v", iParam, reflect.TypeOf (namedval.Value)))
		}

		switch typedval := namedval.Value.(type) {

			// Convert native Go data types into Teradata-specific data type

			case nil     : aBindValues [iParam].Value = NewTeradataNullVarchar   () // null
			case int8    : aBindValues [iParam].Value = NewTeradataValueByteint  (typedval)
			case int16   : aBindValues [iParam].Value = NewTeradataValueSmallint (typedval)
			case int32   : aBindValues [iParam].Value = NewTeradataValueInteger  (typedval)
			case int64   : aBindValues [iParam].Value = NewTeradataValueBigint   (typedval)
			case float64 : aBindValues [iParam].Value = NewTeradataValueFloat    (typedval)
			case [] byte : aBindValues [iParam].Value = NewTeradataValueVarbyte  (typedval)
			case string  : aBindValues [iParam].Value = NewTeradataValueVarchar  (typedval)

			// Convert Go sql package data types into Teradata-specific data type

			case sql.RawBytes: aBindValues [iParam].Value = NewTeradataValueVarbyte (typedval)

			case sql.NullInt64:

				if typedval.Valid {
					aBindValues [iParam].Value = NewTeradataValueBigint (typedval.Int64)
				} else {
					aBindValues [iParam].Value = NewTeradataNullBigint ()
				}

			case sql.NullFloat64:

				if typedval.Valid {
					aBindValues [iParam].Value = NewTeradataValueFloat (typedval.Float64)
				} else {
					aBindValues [iParam].Value = NewTeradataNullFloat ()
				}

			case sql.NullString:

				if typedval.Valid {
					aBindValues [iParam].Value = NewTeradataValueVarchar (typedval.String)
				} else {
					aBindValues [iParam].Value = NewTeradataNullVarchar ()
				}

			case TeradataValue:

				if typedval.m_err != nil {
					err = rows.m_stmt.m_con.makeDriverError (typedval.m_err, 0, fmt.Sprintf ("Parameter %v is an invalid value", iParam + 1))
					return
				}

				// If we are using DataInfo parcel rather than StatementInfo parcel, then substitute
				// VARCHAR in place of all the new data types that are not supported by DataInfo parcel.
				if ! rows.m_bSendSIP {
					switch typedval.CookedDataType () {
						case T_DATE_ANSI                 ,
							 T_TIME                      ,
							 T_TIMESTAMP                 ,
							 T_TIME_WITH_TIME_ZONE       ,
							 T_TIMESTAMP_WITH_TIME_ZONE  ,
							 T_INTERVAL_YEAR             ,
							 T_INTERVAL_YEAR_TO_MONTH    ,
							 T_INTERVAL_MONTH            ,
							 T_INTERVAL_DAY              ,
							 T_INTERVAL_DAY_TO_HOUR      ,
							 T_INTERVAL_DAY_TO_MINUTE    ,
							 T_INTERVAL_DAY_TO_SECOND    ,
							 T_INTERVAL_HOUR             ,
							 T_INTERVAL_HOUR_TO_MINUTE   ,
							 T_INTERVAL_HOUR_TO_SECOND   ,
							 T_INTERVAL_MINUTE           ,
							 T_INTERVAL_MINUTE_TO_SECOND ,
							 T_INTERVAL_SECOND           :

							if typedval.Valid () {
								aBindValues [iParam].Value = NewTeradataValueVarchar (typedval.String ())
							} else {
								aBindValues [iParam].Value = NewTeradataNullVarchar ()
							}
					} // end switch
				} // end if ! rows.m_bSendSIP

			default:
				err = rows.m_stmt.m_con.makeDriverError (nil, 0, fmt.Sprintf ("Parameter %v is unexpected Go data type %v", iParam + 1, reflect.TypeOf (typedval)))
				return

		} // end switch typedval

		teraval := aBindValues [iParam].Value.(TeradataValue)
		if useDeferredLob (&teraval) {
			aBindValues [iParam].Value = NewTeradataValueLobDeferred (deferredLobDataType (teraval.CookedDataType ()), rows.m_stmt.m_con.registerDeferredLob (teraval.m_abyValue))
		}

		err = rows.validateTeradataValue (aBindValues [iParam].Value.(TeradataValue), iParam)
		if err != nil { return }

		rows.setBindMetadata (aBindValues [iParam].Value.(TeradataValue), &aBindMetadata [iParam], 0, iParam, rows.m_mapParameterDataTypes [iParam + 1])

	} // end for

	return

} // end method validateBindValues

func useDeferredLob (teraval *TeradataValue) bool {

	if teraval.m_bPresent {
		switch teraval.CookedDataType () {
			case T_VARBYTE                : return teraval.ByteCount () > 64000
			case T_VARCHAR, T_XML, T_JSON : return teraval.ByteCount () > 32000 // because we use UTF8 session character set
		}
	}

	return false

} // end useDeferredLob

func (rows *TeradataRows) validateBatchValues (aBindValues [] driver.NamedValue) (aBindMetadata [] bindMetadata, err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter validateBatchValues %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave validateBatchValues %v", rows)) } ()
	}

	aaBatchValues := aBindValues [0].Value.([][] any)

	if len (aaBatchValues) == 0 {
		err = rows.m_stmt.m_con.makeDriverError (nil, 500, "Empty batch contains no rows")
		return
	}

	aBindMetadata = make ([] bindMetadata, len (aaBatchValues [0]))
	aaNamedBatchValues := make ([][] driver.NamedValue, len (aaBatchValues))

	aBindValues [0] = driver.NamedValue { Ordinal : 1, Value : aaNamedBatchValues }

	if len (aaBatchValues [0]) > 0xFFFF {
		err = rows.m_stmt.m_con.makeDriverError (nil, 0, fmt.Sprintf ("Too many bound parameter values: %v", len (aaBatchValues [0])))
		return
	}

	for iBindRow, aBindValues := range aaBatchValues {

		if len (aaBatchValues [iBindRow]) != len (aaBatchValues [0]) {

			err = rows.m_stmt.m_con.makeDriverError (nil, 501,
				fmt.Sprintf ("Batch row 1 bound parameter count %v differs from batch row %v bound parameter count %v",
					len (aaBatchValues [0]),
					iBindRow + 1,
					len (aaBatchValues [iBindRow])))

			return
		}

		for nParameter, escapeParamTypeMetadata := range rows.m_mapParameterDataTypes {

			if (nParameter > len (aBindValues)) {

				err = rows.m_stmt.m_con.makeDriverError (nil, 0,
					fmt.Sprintf ("Parameter number %v specified in the teradata_parameter escape function exceeds the parameter count %v",
					nParameter,
					len (aBindValues)))

				return
			}

			err = rows.validateMappedValue (nParameter, escapeParamTypeMetadata.m_uCookedDataType, aBindValues [nParameter - 1])
			if err != nil { return }

			aBindValues [nParameter - 1] = rows.convertMappedValue (escapeParamTypeMetadata, &aBindMetadata [nParameter - 1], aBindValues [nParameter - 1])

		} // end for range rows.m_mapParameterDataTypes

		// For a batch operation, which is a two dimensional array,
		// we must convert all values in the array to driver.NamedValue to be compatible with other funcs
		aNamedBindValues := make ([] driver.NamedValue, len (aBindValues))
		for iParam, interfaceval := range aBindValues {

			if rows.m_stmt.m_con.hasDebugLog () {
				debugLog (fmt.Sprintf ("validateBatchValues: iParam=%v TypeOf=%v", iParam, reflect.TypeOf (interfaceval)))
			}

			switch typedval := interfaceval.(type) {

				// Convert native Go data types into Teradata-specific data type

				case int8    : aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : NewTeradataValueByteint  (typedval)}
				case int16   : aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : NewTeradataValueSmallint (typedval)}
				case int32   : aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : NewTeradataValueInteger  (typedval)}
				case int64   : aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : NewTeradataValueBigint   (typedval)}
				case float64 : aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : NewTeradataValueFloat    (typedval)}
				case [] byte : aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : NewTeradataValueVarbyte  (typedval)}
				case string  : aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : NewTeradataValueVarchar  (typedval)}

				// Convert Go sql package data types into Teradata-specific data type

				case sql.RawBytes: aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : NewTeradataValueVarbyte (typedval)}

				case sql.NullInt64:

					if typedval.Valid {
						aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : NewTeradataValueBigint (typedval.Int64)}
					} else {
						aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : NewTeradataNullBigint ()}
					}

				case sql.NullFloat64:

					if typedval.Valid {
						aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : NewTeradataValueFloat (typedval.Float64)}
					} else {
						aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : NewTeradataNullFloat ()}
					}

				case sql.NullString:

					if typedval.Valid {
						aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : NewTeradataValueVarchar (typedval.String)}
					} else {
						aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : NewTeradataNullVarchar ()}
					}

				case TeradataValue:

					if !rows.m_bSendSIP {
						switch typedval.CookedDataType () {
							case T_DATE_ANSI                 ,
								 T_TIME                      ,
								 T_TIMESTAMP                 ,
								 T_TIME_WITH_TIME_ZONE       ,
								 T_TIMESTAMP_WITH_TIME_ZONE  ,
								 T_INTERVAL_YEAR             ,
								 T_INTERVAL_YEAR_TO_MONTH    ,
								 T_INTERVAL_MONTH            ,
								 T_INTERVAL_DAY              ,
								 T_INTERVAL_DAY_TO_HOUR      ,
								 T_INTERVAL_DAY_TO_MINUTE    ,
								 T_INTERVAL_DAY_TO_SECOND    ,
								 T_INTERVAL_HOUR             ,
								 T_INTERVAL_HOUR_TO_MINUTE   ,
								 T_INTERVAL_HOUR_TO_SECOND   ,
								 T_INTERVAL_MINUTE           ,
								 T_INTERVAL_MINUTE_TO_SECOND ,
								 T_INTERVAL_SECOND           :

								// These data types are not compatible with the DataInfo Parcel and must be converted to a string
								if typedval.Valid () {
									aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : NewTeradataValueVarchar (typedval.String ()) }
								} else {
									aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : NewTeradataNullVarchar () }
								}

							default:
								aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : typedval}

						} // end switch

					} else { // end if not rows.m_bSendSIP

						aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : typedval}
					}

					if typedval.m_err != nil {
						err = rows.m_stmt.m_con.makeDriverError (typedval.m_err, 0, fmt.Sprintf ("Parameter %v is an invalid value", iParam + 1))
						return
					}

				case nil:

					if iBindRow > 0 {
						aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : newTeradataBoundNull (
							aBindMetadata [iParam].m_uCookedDataType,
							aBindMetadata [iParam].m_uPrecision,
							0, // uDigits
							aBindMetadata [iParam].m_uScale,
							aBindMetadata [iParam].m_uByteCount) }
					} else {
						aNamedBindValues [iParam] = driver.NamedValue { Ordinal : iParam + 1, Value : NewTeradataNullVarchar () }
					}

				default:

					err = rows.m_stmt.m_con.makeDriverError (nil, 0, fmt.Sprintf ("Parameter %v is unexpected Go data type %v", iParam + 1, reflect.TypeOf (typedval)))
					return
			}

			teraval := aNamedBindValues [iParam].Value.(TeradataValue)

			switch teraval.CookedDataType () {

				case T_VARBYTE,
					 T_VARCHAR,
					 T_JSON,
					 T_XML:

					uDeferredLobDataType := deferredLobDataType (teraval.CookedDataType ())

					if useDeferredLob (&teraval) {

						teraval = NewTeradataValueLobDeferred (uDeferredLobDataType, rows.m_stmt.m_con.registerDeferredLob (teraval.m_abyValue))

						aNamedBindValues [iParam].Value = teraval

					} else if iBindRow > 0 && aaNamedBatchValues [iBindRow - 1][iParam].Value.(TeradataValue).CookedDataType () == uDeferredLobDataType {

						if rows.m_stmt.m_con.hasDebugLog () {
							debugLog (fmt.Sprintf ("validateBatchValues: Updating data type %v to %v: row=%v col=%v new uCookedDataType=%v old uCookedDataType=%v new uByteCount %v", teraval.CookedDataType (), uDeferredLobDataType, iBindRow, (iParam + 1), teraval.CookedDataType (), aaNamedBatchValues [iBindRow - 1][iParam].Value.(TeradataValue).CookedDataType (), teraval.ByteCount ()))
						}

						teraval = func () TeradataValue { if teraval.Valid () {
							return NewTeradataValueLobDeferred (uDeferredLobDataType, rows.m_stmt.m_con.registerDeferredLob (teraval.m_abyValue)) }
							return NewTeradataNullLobDeferred (uDeferredLobDataType) } ()

						aNamedBindValues [iParam].Value = teraval
					}
			} // end switch

			// Validate that the bind data type is either the same as in previous rows or the
			// bound values for the previous rows in the batch were null and set to VARCHAR or
			// the bound values for the previous rows in the batch were VARBYTE/VARCHAR and the
			// current row is a deferred LOB.

			if iBindRow > 0 && aBindMetadata [iParam].m_uCookedDataType != teraval.CookedDataType () {

				var iRowInBatch int
				for iRowInBatch = iBindRow - 1 ; iRowInBatch >= 0 ; iRowInBatch-- {

					// Set the bound value to null based on the current non-null bin value's data type
					if rows.m_stmt.m_con.hasDebugLog () {
						debugLog (fmt.Sprintf ("validateBatchValues: Updating null value: row=%v col=%v new uCookedDataType=%v old uCookedDataType=%v new uByteCount %v new uScale %v", iRowInBatch + 1, iParam + 1, teraval.CookedDataType (), aaNamedBatchValues [iRowInBatch][iParam].Value.(TeradataValue).CookedDataType (), teraval.ByteCount (), teraval.Scale ()))
					}

					previousTeraval := aaNamedBatchValues [iRowInBatch][iParam].Value.(TeradataValue)

					// need bytecount for null types of fixed length
					if  ! previousTeraval.Valid () {

						// Set the bound value to null based on the current non-null bin value's data type
						if rows.m_stmt.m_con.hasDebugLog () {
							debugLog (fmt.Sprintf ("validateBatchValues: Updating null value: row=%v col=%v new uCookedDataType=%v old uCookedDataType=%v new uByteCount %v new uScale %v", (iRowInBatch + 1), (iParam + 1), teraval.CookedDataType (), previousTeraval.CookedDataType (), teraval.ByteCount (), teraval.Scale ()))
						}

						aaNamedBatchValues [iRowInBatch][iParam].Value = newTeradataBoundNull (teraval.CookedDataType (), /*uPrecision*/ 0, /*uDigits*/ 0, teraval.Scale (), teraval.ByteCount ())

					} else if isDeferredLobType (teraval.CookedDataType ()) &&
						! isDeferredLobType (previousTeraval.CookedDataType ()) &&
						teraval.CookedDataType () == deferredLobDataType (previousTeraval.CookedDataType ()) {

						if rows.m_stmt.m_con.hasDebugLog () {
							debugLog (fmt.Sprintf ("validateBatchValues: Updating data type: row=%v col=%v new uCookedDataType=%v old uCookedDataType=%v new uByteCount %v new uScale %v", (iRowInBatch + 1), (iParam + 1), teraval.CookedDataType (), previousTeraval.CookedDataType (), teraval.ByteCount (), teraval.Scale ()))
						}

						// Set the bound value to deferred LOB if current bin value's data type is deferred LOB and the previous data type was a varbyte/varchar/json/xml
						aaNamedBatchValues [iRowInBatch][iParam].Value = NewTeradataValueLobDeferred (teraval.CookedDataType (), rows.m_stmt.m_con.registerDeferredLob (previousTeraval.m_abyValue))

					} else {
						break
					}
				} // end for loop

				// If iRowInBatch is less than 0, then the bind values for all previous rows were null or in the case of deferred
				// LOBs, were all varbyte/varchar, so reset the bind metadata. Otherwise create an error for mismatched data types
				if iRowInBatch < 0 {
					aBindMetadata [iParam] = bindMetadata {}
				} else {
					_, _, _, _, sTeravalTypeName := dataTypeInfo (teraval.CookedDataType ())
					_, _, _, _, sParamTypeName   := dataTypeInfo (aBindMetadata [iParam].m_uCookedDataType)

					err = rows.m_stmt.m_con.makeDriverError (nil, 502,
						fmt.Sprintf ("Batch row %v bound parameter %v type %v (%v) differs from batch row 1 type %v (%v)",
							iBindRow + 1,
							iParam + 1,
							sTeravalTypeName,
							teraval.CookedDataType (),
							sParamTypeName,
							aBindMetadata [iParam].m_uCookedDataType))

					return
				}
			}

			err = rows.validateTeradataValue (teraval, iParam)
			if err != nil { return }

			rows.setBindMetadata (teraval, &aBindMetadata [iParam], iBindRow, iParam, rows.m_mapParameterDataTypes [iParam + 1])

		} // end for iParam

		aaNamedBatchValues [iBindRow] = aNamedBindValues

	} // end for iBindRow

	// Adjust batch bind values. The fixed-length bind values must be the same for each parameter in the parameter set.
	for nParSet, aBindValues := range aaNamedBatchValues {

		for nVal, namedval := range aBindValues {

			teraval := namedval.Value.(TeradataValue)

			switch teraval.CookedDataType () {

				case T_CHAR                      ,
					 T_BYTE                      ,
					 T_TIME                      ,
					 T_TIME_WITH_TIME_ZONE       ,
					 T_TIMESTAMP                 ,
					 T_TIMESTAMP_WITH_TIME_ZONE  ,
					 T_INTERVAL_YEAR             ,
					 T_INTERVAL_YEAR_TO_MONTH    ,
					 T_INTERVAL_MONTH            ,
					 T_INTERVAL_DAY              ,
					 T_INTERVAL_DAY_TO_HOUR      ,
					 T_INTERVAL_DAY_TO_MINUTE    ,
					 T_INTERVAL_DAY_TO_SECOND    ,
					 T_INTERVAL_HOUR             ,
					 T_INTERVAL_HOUR_TO_MINUTE   ,
					 T_INTERVAL_HOUR_TO_SECOND   ,
					 T_INTERVAL_MINUTE           ,
					 T_INTERVAL_MINUTE_TO_SECOND ,
					 T_INTERVAL_SECOND           :

					if teraval.ByteCount () < aBindMetadata [nVal].m_uByteCount {
						aaNamedBatchValues [nParSet][nVal].Value = teraval.adjustByteLength (rows.m_stmt.m_con, aBindMetadata [nVal].m_uByteCount, aBindMetadata [nVal].m_uScale - teraval.Scale ())
					}

				case T_DECIMAL:

					if teraval.m_bPresent && teraval.Scale () < aBindMetadata [nVal].m_uScale {

						teraval.adjustDecimalScale (aBindMetadata [nVal].m_uPrecision, aBindMetadata [nVal].m_uScale)

						if teraval.m_err != nil {

							err = rows.m_stmt.m_con.makeDriverError (teraval.m_err, 0,
								fmt.Sprintf ("Batch row %v bound parameter %v is an invalid value",
									nParSet + 1,
									nVal + 1))

							return

						} else {
							aaNamedBatchValues [nParSet][nVal].Value = teraval
						}
					} else if teraval.ByteCount () < aBindMetadata [nVal].m_uByteCount {
						aaNamedBatchValues [nParSet][nVal].Value = teraval.adjustByteLength (rows.m_stmt.m_con, aBindMetadata [nVal].m_uByteCount, aBindMetadata [nVal].m_uPrecision)
					}
			} // end switch
		} // end for nVal
	} // end for nParSet

	return

} // end method validateBatchValues

func (rows *TeradataRows) validateCSVFileValues (aBindValues [] driver.NamedValue) (aBindMetadata [] bindMetadata, err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter validateCSVFileValues %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave validateCSVFileValues %v", rows)) } ()
	}

	aaBatchValues := aBindValues [0].Value.([][] driver.NamedValue)

	aBindMetadata = make ([] bindMetadata, len (aaBatchValues [0]))

	if len (aaBatchValues [0]) > 0xFFFF {
		err = rows.m_stmt.m_con.makeDriverError (nil, 0, fmt.Sprintf ("Too many bound parameter values: %v", len (aaBatchValues [0])))
		return
	}

	for iBindRow, aBindValues := range aaBatchValues {

		for iParam, interfaceval := range aBindValues {

			teraval := interfaceval.Value.(TeradataValue)

			// If the VARACHAR length is greater than 32000, then treat as a deferred CLOB value
			if teraval.m_bPresent && teraval.ByteCount () > 32000 {

				teraval = NewTeradataValueLobDeferred (T_CLOB_AS_DEFERRED, rows.m_stmt.m_con.registerDeferredLob (teraval.m_abyValue))
				if rows.m_stmt.m_con.hasDebugLog () {
					debugLog (fmt.Sprintf ("validateCSVFileValues: Updating data type: row=%v col=%v new uCookedDataType=%v old uCookedDataType=%v", (iBindRow + 1), (iParam + 1), teraval.CookedDataType (), T_VARCHAR))
				}
				aaBatchValues [iBindRow] [iParam].Value = teraval

			} else if iBindRow > 0 && aaBatchValues [iBindRow - 1][iParam].Value.(TeradataValue).CookedDataType () == T_CLOB_AS_DEFERRED {

				if rows.m_stmt.m_con.hasDebugLog () {
					debugLog (fmt.Sprintf ("validateCSVFileValues: Updating data type %v to %v: row=%v col=%v new uCookedDataType=%v old uCookedDataType=%v new uByteCount %v", teraval.CookedDataType (), T_CLOB_AS_DEFERRED, iBindRow, (iParam + 1), teraval.CookedDataType (), aaBatchValues [iBindRow - 1][iParam].Value.(TeradataValue).CookedDataType (), teraval.ByteCount ()))
				}

				teraval = func () TeradataValue { if teraval.Valid () {
					return NewTeradataValueLobDeferred (T_CLOB_AS_DEFERRED, rows.m_stmt.m_con.registerDeferredLob (teraval.m_abyValue)) }
					return NewTeradataNullLobDeferred (T_CLOB_AS_DEFERRED) } ()

				aaBatchValues [iBindRow] [iParam].Value = teraval
			}

			// Validate that the bind data type is either the same as in previous rows or the
			// bound values for the previous rows in the batch were null and set to VARCHAR or
			// the bound values for the previous rows in the batch were VARBYTE/VARCHAR and the
			// current row is a deferred LOB.

			if iBindRow > 0 && aBindMetadata [iParam].m_uCookedDataType != teraval.CookedDataType () {

				var iRowInBatch int
				for iRowInBatch = iBindRow - 1 ; iRowInBatch >= 0 ; iRowInBatch-- {

					// Set the bound value to null based on the current non-null bin value's data type
					if rows.m_stmt.m_con.hasDebugLog () {
						debugLog (fmt.Sprintf ("validateCSVFileValues: Updating null value: row=%v col=%v new uCookedDataType=%v old uCookedDataType=%v new uByteCount %v new uScale %v", iRowInBatch + 1, iParam + 1, teraval.CookedDataType (), aaBatchValues [iRowInBatch][iParam].Value.(TeradataValue).CookedDataType (), teraval.ByteCount (), teraval.Scale ()))
					}

					previousTeraval := aaBatchValues [iRowInBatch][iParam].Value.(TeradataValue)
					if ! previousTeraval.Valid () {

						// Set the bound value to null based on the current non-null bin value's data type
						if rows.m_stmt.m_con.hasDebugLog () {
							debugLog (fmt.Sprintf ("validateCSVFileValues: Updating null value: row=%v col=%v new uCookedDataType=%v old uCookedDataType=%v new uByteCount %v new uScale %v", (iRowInBatch + 1), (iParam + 1), teraval.CookedDataType (), previousTeraval.CookedDataType (), teraval.ByteCount (), teraval.Scale ()))
						}

						aaBatchValues [iRowInBatch][iParam].Value = newTeradataBoundNull (teraval.CookedDataType (), /*uPrecision*/ 0, /*uDigits*/ 0, teraval.Scale (), teraval.ByteCount ())

					} else	if isDeferredLobType (teraval.CookedDataType ()) &&
						! isDeferredLobType (previousTeraval.CookedDataType ()) &&
						teraval.CookedDataType () == deferredLobDataType (previousTeraval.CookedDataType ()) {

						if rows.m_stmt.m_con.hasDebugLog () {
							debugLog (fmt.Sprintf ("validateCSVFileValues: Updating data type: row=%v col=%v new uCookedDataType=%v old uCookedDataType=%v new uByteCount %v new uScale %v", (iRowInBatch + 1), (iParam + 1), teraval.CookedDataType (), previousTeraval.CookedDataType (), teraval.ByteCount (), teraval.Scale ()))
						}

						// Set the bound value to deferred LOB if current bin value's data type is deferred LOB and the previous data type was a varbyte/varchar/json/xml
						aaBatchValues [iRowInBatch][iParam].Value = NewTeradataValueLobDeferred (teraval.CookedDataType (), rows.m_stmt.m_con.registerDeferredLob (previousTeraval.m_abyValue))

					} else {
						break
					}
				} // end for loop

				// If iRowInBatch is less than 0, then the bind values for all previous rows were null or in the case of deferred
				// LOBs, were all varchar, so reset the bind metadata.
				if iRowInBatch < 0 {
					aBindMetadata [iParam] = bindMetadata {}
				}
			}
			rows.setBindMetadata (teraval, &aBindMetadata [iParam], iBindRow, iParam, rows.m_mapParameterDataTypes [iParam + 1])

			if rows.m_stmt.m_con.hasDebugLog () {
				debugLog (fmt.Sprintf ("validateCSVFileValues: iBindRow=%v iParam=%v uCookedDataType=%v", iBindRow, iParam, teraval.CookedDataType ()))
			}

		} // end for iParam

	} // end for iBindRow

	return

} // end method validateCSVFileValues

func (rows *TeradataRows) validateParquetFileValues (aBindValues [] driver.NamedValue) (aBindMetadata [] bindMetadata, err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter validateParquetFileValues %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave validateParquetFileValues %v", rows)) } ()
	}

	aaBatchValues := aBindValues [0].Value.([][] driver.NamedValue)

	aBindMetadata = make ([] bindMetadata, len (aaBatchValues [0]))

	if len (aaBatchValues [0]) > 0xFFFF {
		err = rows.m_stmt.m_con.makeDriverError (nil, 0, fmt.Sprintf ("Too many bound parameter values: %v", len (aaBatchValues [0])))
		return
	}

	for iBindRow, aBindValues := range aaBatchValues {

		for iParam, interfaceval := range aBindValues {

			teraval := interfaceval.Value.(TeradataValue)

			// Check if type mapping is specified for this parameter
			if mappedDataType, ok := rows.m_mapParameterDataTypes [iParam + 1] ; ok {

				// Get the underlying Go value from the TeradataValue
				baseValue := teraval.AsGoType ()

				// Validate that the base value is compatible with the mapped type
				if err = rows.validateMappedValue (iParam + 1, mappedDataType.m_uCookedDataType, baseValue) ; err != nil {
					return
				}

				// Convert the base value to a TeradataValue of the mapped type
				teraval = rows.convertMappedValue (mappedDataType, &aBindMetadata [iParam], baseValue)

				// Replace the TeradataValue with the converted one
				aaBatchValues [iBindRow] [iParam].Value = teraval
			}

			// If the VARCHAR length is greater than 32000, then treat as a deferred CLOB value
			// If the VARBYTE length is greater than 64000, then treat as a deferred BLOB value
			if teraval.m_bPresent && teraval.CookedDataType () == T_VARCHAR && teraval.ByteCount () > 32000 {

				teraval = NewTeradataValueLobDeferred (T_CLOB_AS_DEFERRED, rows.m_stmt.m_con.registerDeferredLob (teraval.m_abyValue))
				if rows.m_stmt.m_con.hasDebugLog () {
					debugLog (fmt.Sprintf ("validateParquetFileValues: Updating data type: row=%v col=%v new uCookedDataType=%v old uCookedDataType=%v", (iBindRow + 1), (iParam + 1), teraval.CookedDataType (), T_VARCHAR))
				}
				aaBatchValues [iBindRow] [iParam].Value = teraval

			} else if teraval.m_bPresent && teraval.CookedDataType () == T_VARBYTE && teraval.ByteCount () > 64000 {

				teraval = NewTeradataValueLobDeferred (T_BLOB_AS_DEFERRED, rows.m_stmt.m_con.registerDeferredLob (teraval.m_abyValue))
				if rows.m_stmt.m_con.hasDebugLog () {
					debugLog (fmt.Sprintf ("validateParquetFileValues: Updating data type: row=%v col=%v new uCookedDataType=%v old uCookedDataType=%v", (iBindRow + 1), (iParam + 1), teraval.CookedDataType (), T_VARBYTE))
				}
				aaBatchValues [iBindRow] [iParam].Value = teraval

			} else if iBindRow > 0 {

				previousTeraval := aaBatchValues [iBindRow - 1][iParam].Value.(TeradataValue)

				if previousTeraval.CookedDataType () == T_CLOB_AS_DEFERRED && (! teraval.m_bPresent || teraval.CookedDataType () == T_VARCHAR) {

					if rows.m_stmt.m_con.hasDebugLog () {
						debugLog (fmt.Sprintf ("validateParquetFileValues: Updating data type %v to %v: row=%v col=%v new uCookedDataType=%v old uCookedDataType=%v new uByteCount %v", teraval.CookedDataType (), T_CLOB_AS_DEFERRED, iBindRow, (iParam + 1), teraval.CookedDataType (), previousTeraval.CookedDataType (), teraval.ByteCount ()))
					}

					teraval = func () TeradataValue { if teraval.Valid () {
						return NewTeradataValueLobDeferred (T_CLOB_AS_DEFERRED, rows.m_stmt.m_con.registerDeferredLob (teraval.m_abyValue)) }
						return NewTeradataNullLobDeferred (T_CLOB_AS_DEFERRED) } ()
					aaBatchValues [iBindRow] [iParam].Value = teraval

				} else if previousTeraval.CookedDataType () == T_BLOB_AS_DEFERRED && (! teraval.m_bPresent || teraval.CookedDataType () == T_VARBYTE) {

					if rows.m_stmt.m_con.hasDebugLog () {
						debugLog (fmt.Sprintf ("validateParquetFileValues: Updating data type %v to %v: row=%v col=%v new uCookedDataType=%v old uCookedDataType=%v new uByteCount %v", teraval.CookedDataType (), T_BLOB_AS_DEFERRED, iBindRow, (iParam + 1), teraval.CookedDataType (), previousTeraval.CookedDataType (), teraval.ByteCount ()))
					}

					teraval = func () TeradataValue { if teraval.Valid () {
						return NewTeradataValueLobDeferred (T_BLOB_AS_DEFERRED, rows.m_stmt.m_con.registerDeferredLob (teraval.m_abyValue)) }
						return NewTeradataNullLobDeferred (T_BLOB_AS_DEFERRED) } ()
					aaBatchValues [iBindRow] [iParam].Value = teraval
				}
			}

			// Validate that the bind data type is either the same as in previous rows or the
			// bound values for the previous rows in the batch were null and set to VARCHAR or
			// the bound values for the previous rows in the batch were VARBYTE/VARCHAR and the
			// current row is a deferred LOB.

			if iBindRow > 0 && aBindMetadata [iParam].m_uCookedDataType != teraval.CookedDataType () {

				var iRowInBatch int
				for iRowInBatch = iBindRow - 1 ; iRowInBatch >= 0 ; iRowInBatch-- {

					// Set the bound value to null based on the current non-null bin value's data type
					if rows.m_stmt.m_con.hasDebugLog () {
						debugLog (fmt.Sprintf ("validateParquetFileValues: Updating null value: row=%v col=%v new uCookedDataType=%v old uCookedDataType=%v new uByteCount %v new uScale %v", iRowInBatch + 1, iParam + 1, teraval.CookedDataType (), aaBatchValues [iRowInBatch][iParam].Value.(TeradataValue).CookedDataType (), teraval.ByteCount (), teraval.Scale ()))
					}

					previousTeraval := aaBatchValues [iRowInBatch][iParam].Value.(TeradataValue)
					if ! previousTeraval.Valid () {

						// Set the bound value to null based on the current non-null bin value's data type
						if rows.m_stmt.m_con.hasDebugLog () {
							debugLog (fmt.Sprintf ("validateParquetFileValues: Updating null value: row=%v col=%v new uCookedDataType=%v old uCookedDataType=%v new uByteCount %v new uScale %v", (iRowInBatch + 1), (iParam + 1), teraval.CookedDataType (), previousTeraval.CookedDataType (), teraval.ByteCount (), teraval.Scale ()))
						}

						aaBatchValues [iRowInBatch][iParam].Value = newTeradataBoundNull (teraval.CookedDataType (), /*uPrecision*/ 0, /*uDigits*/ 0, teraval.Scale (), teraval.ByteCount ())

					} else	if isDeferredLobType (teraval.CookedDataType ()) &&
						! isDeferredLobType (previousTeraval.CookedDataType ()) &&
						teraval.CookedDataType () == deferredLobDataType (previousTeraval.CookedDataType ()) {

						if rows.m_stmt.m_con.hasDebugLog () {
							debugLog (fmt.Sprintf ("validateParquetFileValues: Updating data type: row=%v col=%v new uCookedDataType=%v old uCookedDataType=%v new uByteCount %v new uScale %v", (iRowInBatch + 1), (iParam + 1), teraval.CookedDataType (), previousTeraval.CookedDataType (), teraval.ByteCount (), teraval.Scale ()))
						}

						// Set the bound value to deferred LOB if current bin value's data type is deferred LOB and the previous data type was a varbyte/varchar/json/xml
						aaBatchValues [iRowInBatch][iParam].Value = NewTeradataValueLobDeferred (teraval.CookedDataType (), rows.m_stmt.m_con.registerDeferredLob (previousTeraval.m_abyValue))

					} else {
						break
					}
				} // end for loop

				// If iRowInBatch is less than 0, then the bind values for all previous rows were null or in the case of deferred
				// LOBs, were all varchar, so reset the bind metadata.
				if iRowInBatch < 0 {
					aBindMetadata [iParam] = bindMetadata {}
				}
			}

			rows.setBindMetadata (teraval, &aBindMetadata [iParam], iBindRow, iParam, rows.m_mapParameterDataTypes [iParam + 1])

			if rows.m_stmt.m_con.hasDebugLog () {
				debugLog (fmt.Sprintf ("validateParquetFileValues: iBindRow=%v iParam=%v uCookedDataType=%v", iBindRow, iParam, teraval.CookedDataType ()))
			}

		} // end for iParam

	} // end for iBindRow

	return

} // end validateParquetFileValues

func (rows *TeradataRows) validateMappedValue (nParameter int, uDataType uint16, oBindValue any) (err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter validateMappedValue %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave validateMappedValue %v", rows)) } ()
	}

	switch uDataType {

		case T_BLOB_AS_LOCATOR,
			T_CLOB_AS_LOCATOR,
			T_JSON_AS_LOCATOR,
			T_XML_AS_LOCATOR:

			if _, ok := oBindValue.([] byte); !ok {

				err = rows.m_stmt.m_con.makeDriverError (nil, 0,
					fmt.Sprintf ("Parameter %v, locator value must be a byte array not %v",
						nParameter,
						reflect.TypeOf (oBindValue)))

				return
			}

		case T_BYTEINT,
			T_SMALLINT,
			T_INTEGER ,
			T_BIGINT  :

			if oBindValue == nil { return }
			if _, ok := oBindValue.(int64) ; ! ok {

				_, _, _, _, sTypeName := dataTypeInfo (uDataType)

				err = rows.m_stmt.m_con.makeDriverError (nil, 573,
					fmt.Sprintf ("Parameter %v, %v value must be an integer not a %v",
						nParameter,
						sTypeName,
						reflect.TypeOf (oBindValue)))

				return
			}

		case T_BYTE          ,
			T_VARBYTE        ,
			T_LONG_VARBYTE   ,
			T_DATASET_AVRO   :

			if oBindValue == nil { return }
			if _, ok := oBindValue.([] byte) ; ! ok {

				_, _, _, _, sTypeName := dataTypeInfo (uDataType)

				err = rows.m_stmt.m_con.makeDriverError (nil, 0,
					fmt.Sprintf ("Parameter %v, %v value must be a byte array not %v",
						nParameter,
						sTypeName,
						reflect.TypeOf (oBindValue)))

				return
			}

		case T_CHAR                          ,
			T_LONG_VARCHAR                   ,
			T_FLOAT                          ,
			T_DECIMAL                        ,
			T_NUMBER                         ,
			T_INTERVAL_YEAR                  ,
			T_INTERVAL_YEAR_TO_MONTH         ,
			T_INTERVAL_MONTH                 ,
			T_INTERVAL_DAY                   ,
			T_INTERVAL_DAY_TO_HOUR           ,
			T_INTERVAL_DAY_TO_MINUTE         ,
			T_INTERVAL_DAY_TO_SECOND         ,
			T_INTERVAL_HOUR                  ,
			T_INTERVAL_HOUR_TO_MINUTE        ,
			T_INTERVAL_HOUR_TO_SECOND        ,
			T_INTERVAL_MINUTE                ,
			T_INTERVAL_MINUTE_TO_SECOND      ,
			T_INTERVAL_SECOND                ,
			T_PERIOD_DATE                    ,
			T_PERIOD_TIME                    ,
			T_PERIOD_TIME_WITH_TIME_ZONE     ,
			T_PERIOD_TIMESTAMP               ,
			T_PERIOD_TIMESTAMP_WITH_TIME_ZONE,
			T_DATE_ANSI                      ,
			T_DATE_INTEGER                   ,
			T_TIME                           ,
			T_TIME_WITH_TIME_ZONE            ,
			T_TIMESTAMP                      ,
			T_TIMESTAMP_WITH_TIME_ZONE       ,
			T_XML                            ,
			T_DATASET_CSV                    ,
			T_JSON                           :

			if oBindValue == nil { return }

			if _, ok := oBindValue.(string); !ok {

				_, _, _, _, sTypeName := dataTypeInfo (uDataType)

				err = rows.m_stmt.m_con.makeDriverError (nil, 0,
					fmt.Sprintf ("Parameter %v %v value must be string not %v",
						nParameter,
						sTypeName,
						reflect.TypeOf (oBindValue)))

				 return
			}

	}

	return

}  // end validateMappedValue

func (rows *TeradataRows) convertMappedValue (escapeParamTypeMetadata escParamDataTypeMetadata, bmd *bindMetadata, oValue any) (tValue TeradataValue) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter convertMappedValue %v %v", rows, escapeParamTypeMetadata))
		defer func () { traceLog (fmt.Sprintf ("< leave convertMappedValue %v %v", rows, tValue.m_uCookedDataType)) } ()
	}

	switch escapeParamTypeMetadata.m_uCookedDataType {

		case T_BLOB_AS_LOCATOR: tValue = NewTeradataValueBlobLocator  (oValue.([]byte))
		case T_CLOB_AS_LOCATOR: tValue = NewTeradataValueClobLocator  (oValue.([]byte))
		case T_JSON_AS_LOCATOR: tValue = NewTeradataValueJsonLocator  (oValue.([]byte))
		case T_XML_AS_LOCATOR : tValue = NewTeradataValueXmlLocator   (oValue.([]byte))

		case T_BYTEINT        : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullByteint ()     } ; return NewTeradataValueByteint  (int8  (oValue.(int64))) } ()
		case T_SMALLINT       : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullSmallint ()    } ; return NewTeradataValueSmallint (int16 (oValue.(int64))) } ()
		case T_INTEGER        : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullInteger ()     } ; return NewTeradataValueInteger  (int32 (oValue.(int64))) } ()
		case T_BIGINT         : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullBigint ()      } ; return NewTeradataValueBigint          (oValue.(int64))  } ()
		case T_XML            : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullXml ()         } ; return NewTeradataValueXml             (oValue.(string)) } ()
		case T_JSON           : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullJson ()        } ; return NewTeradataValueJson            (oValue.(string)) } ()

		case T_LONG_VARBYTE   : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullLongVarbyte () } ; return NewTeradataValueLongVarbyte     (oValue.([]byte)) } ()
		case T_LONG_VARCHAR   : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullLongVarchar () } ; return NewTeradataValueLongVarchar     (oValue.(string)) } ()
		case T_VARBYTE        : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullVarbyte () } ; return NewTeradataValueVarbyte (oValue.([]byte)) } ()
		case T_VARCHAR        : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullVarchar () } ; return NewTeradataValueVarchar (oValue.(string)) } ()


		case T_BYTE :
			tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullByte    (escapeParamTypeMetadata.m_uByteCount) } ; return NewTeradataValueByte (oValue.([]byte)) } ()
			if tValue.m_bPresent && tValue.m_uByteCount < escapeParamTypeMetadata.m_uByteCount {
				tValue = tValue.adjustByteLength (rows.m_stmt.m_con, escapeParamTypeMetadata.m_uByteCount - tValue.m_uByteCount, 0)
			}

		case T_CHAR :
			tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullChar    (escapeParamTypeMetadata.m_uByteCount) } ; return NewTeradataValueChar (oValue.(string)) } ()
			if tValue.m_bPresent && tValue.m_uByteCount < escapeParamTypeMetadata.m_uByteCount {
				tValue = tValue.adjustByteLength (rows.m_stmt.m_con, escapeParamTypeMetadata.m_uByteCount - tValue.m_uByteCount, 0)
			}

		case T_FLOAT :
			tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullFloat () } ; return ParseTeradataValueFloat (oValue.(string)) } ()

		case T_NUMBER :
			tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullNumber () } ; return ParseTeradataValueNumber (oValue.(string)) } ()

		case T_DECIMAL :
				if oValue == nil {
					tValue = NewTeradataNullDecimal (escapeParamTypeMetadata.m_uPrecision, escapeParamTypeMetadata.m_uScale)
				} else {
					tValue = ParseTeradataValueDecimal (oValue.(string))

					// adjustDecimalScale calls adjustByteLength so it is not necessary to call adjustByteLength if we call adjustDecimalScale
					if tValue.Scale () < escapeParamTypeMetadata.m_uScale {
						tValue.adjustDecimalScale (escapeParamTypeMetadata.m_uPrecision, escapeParamTypeMetadata.m_uScale)
					} else if tValue.m_uByteCount < byteCountFromDecimalPrecision (escapeParamTypeMetadata.m_uPrecision) {
						tValue = tValue.adjustByteLength (rows.m_stmt.m_con, tValue.m_uByteCount, escapeParamTypeMetadata.m_uPrecision)
					}
				}

		case T_INTERVAL_YEAR            ,
			T_INTERVAL_YEAR_TO_MONTH    ,
			T_INTERVAL_MONTH            ,
			T_INTERVAL_DAY              ,
			T_INTERVAL_DAY_TO_HOUR      ,
			T_INTERVAL_DAY_TO_MINUTE    ,
			T_INTERVAL_DAY_TO_SECOND    ,
			T_INTERVAL_HOUR             ,
			T_INTERVAL_HOUR_TO_MINUTE   ,
			T_INTERVAL_HOUR_TO_SECOND   ,
			T_INTERVAL_MINUTE           ,
			T_INTERVAL_MINUTE_TO_SECOND ,
			T_INTERVAL_SECOND           :

			if oValue == nil {
				tValue = newTeradataBoundNull (escapeParamTypeMetadata.m_uCookedDataType, 0, uint64 (escapeParamTypeMetadata.m_uIntervalDigits), escapeParamTypeMetadata.m_uScale, 0)
			} else {
				tValue = newTeradataValue (escapeParamTypeMetadata.m_uCookedDataType, 0, []byte (oValue.(string)))
			}

		case T_PERIOD_DATE                     : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullPeriodDate                  ()                             } ; return ParseTeradataValuePeriodDate                  (oValue.(string)) } ()
		case T_PERIOD_TIME                     : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullPeriodTime                  (escapeParamTypeMetadata.m_uScale) } ; return ParseTeradataValuePeriodTime                  (oValue.(string)) } ()
		case T_PERIOD_TIME_WITH_TIME_ZONE      : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullPeriodTimeWithTimeZone      (escapeParamTypeMetadata.m_uScale) } ; return ParseTeradataValuePeriodTimeWithTimeZone      (oValue.(string)) } ()
		case T_PERIOD_TIMESTAMP                : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullPeriodTimestamp             (escapeParamTypeMetadata.m_uScale) } ; return ParseTeradataValuePeriodTimestamp             (oValue.(string)) } ()
		case T_PERIOD_TIMESTAMP_WITH_TIME_ZONE : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullPeriodTimestampWithTimeZone (escapeParamTypeMetadata.m_uScale) } ; return ParseTeradataValuePeriodTimestampWithTimeZone (oValue.(string)) } ()
		case T_DATE_ANSI                       : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullDateANSI                    ()                             } ; return NewTeradataValueDateANSI                      (oValue.(string)) } ()
		case T_DATE_INTEGER                    : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullDateInteger                 ()                             } ; return ParseTeradataValueDateInteger                 (oValue.(string)) } ()
		case T_TIME                            : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullTime                        (escapeParamTypeMetadata.m_uScale) } ; return NewTeradataValueTime                  (oValue.(string)) } ()
		case T_TIME_WITH_TIME_ZONE             : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullTimeWithTimeZone            (escapeParamTypeMetadata.m_uScale) } ; return NewTeradataValueTimeWithTimeZone      (oValue.(string)) } ()
		case T_TIMESTAMP                       : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullTimestamp                   (escapeParamTypeMetadata.m_uScale) } ; return NewTeradataValueTimestamp             (oValue.(string)) } ()
		case T_TIMESTAMP_WITH_TIME_ZONE        : tValue = func () TeradataValue { if oValue == nil { return NewTeradataNullTimestampWithTimeZone       (escapeParamTypeMetadata.m_uScale) } ; return NewTeradataValueTimestampWithTimeZone (oValue.(string)) } ()

		case T_DATASET_CSV :

			bmd.m_uServerDataType = escapeParamTypeMetadata.m_uCookedDataType | 1
			if oValue == nil {
				tValue = NewTeradataNullLobDeferred (T_CLOB_AS_DEFERRED)
			} else {
				tValue = NewTeradataValueLobDeferred (T_CLOB_AS_DEFERRED, rows.m_stmt.m_con.registerDeferredLob ([] byte (oValue.(string))))
			}

		case T_DATASET_AVRO :

			bmd.m_uServerDataType = escapeParamTypeMetadata.m_uCookedDataType | 1
			if oValue == nil {
				tValue = NewTeradataNullLobDeferred (T_BLOB_AS_DEFERRED)
			} else {
				tValue = NewTeradataValueLobDeferred (T_BLOB_AS_DEFERRED, rows.m_stmt.m_con.registerDeferredLob (oValue.([] byte)))
			}
	} // end switch

	return

} // end convertMappedValue

func (rows *TeradataRows) validateTeradataValue (teraval TeradataValue, iParam int) (err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter validateTeradataValue %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave validateTeradataValue %v", rows)) } ()
	}

	uCookedDataType := teraval.CookedDataType ()

	// Verify all bound data values are expected Teradata wire protocol data types

	nPrefixByteCount, _, _, bCanBind, sTypeName := dataTypeInfo (uCookedDataType)

	if ! bCanBind {

		err = rows.m_stmt.m_con.makeDriverError (nil, 0,
			fmt.Sprintf ("Parameter %v has unsupported SQL data type %v %v",
				iParam + 1,
				sTypeName,
				uCookedDataType))

		return
	}

	// if session character set is not UTF8
	// then reserialize all character values from UTF8 into the session character set

	uMaxValueLength := func () uint64 { if nPrefixByteCount == 1 { return 0xFF } ; return 0xFFFF } ()

	if teraval.ByteCount () > uMaxValueLength {

		err = rows.m_stmt.m_con.makeDriverError (nil, 0,
			fmt.Sprintf ("Parameter %v SQL data type %v %v length %v is too large",
				iParam + 1,
				sTypeName,
				uCookedDataType,
				teraval.ByteCount ()))

		return
	}

	switch uCookedDataType {

		case T_DECIMAL:

			switch teraval.ByteCount () {

				case 1, 2, 4, 8, 16: // do nothing

				default:
					err = rows.m_stmt.m_con.makeDriverError (nil, 0,
						fmt.Sprintf ("Parameter %v SQL data type %v %v invalid length %v",
							iParam + 1,
							sTypeName,
							uCookedDataType,
							teraval.ByteCount ()))

					return
			}

		case T_NUMBER:

			// 2 byte scale followed by 1 to 17 bytes unscaled value
			if teraval.ByteCount () > 19 {

				err = rows.m_stmt.m_con.makeDriverError (nil, 0,
					fmt.Sprintf ("Parameter %v SQL data type %v %v length %v is too large",
						iParam + 1,
						sTypeName,
						uCookedDataType,
						teraval.ByteCount ()))

				return
			}

		case T_TIME                      ,
			 T_TIMESTAMP                 ,
			 T_TIME_WITH_TIME_ZONE       ,
			 T_TIMESTAMP_WITH_TIME_ZONE  ,
			 T_INTERVAL_YEAR             ,
			 T_INTERVAL_YEAR_TO_MONTH    ,
			 T_INTERVAL_MONTH            ,
			 T_INTERVAL_DAY              ,
			 T_INTERVAL_DAY_TO_HOUR      ,
			 T_INTERVAL_DAY_TO_MINUTE    ,
			 T_INTERVAL_DAY_TO_SECOND    ,
			 T_INTERVAL_HOUR             ,
			 T_INTERVAL_HOUR_TO_MINUTE   ,
			 T_INTERVAL_HOUR_TO_SECOND   ,
			 T_INTERVAL_MINUTE           ,
			 T_INTERVAL_MINUTE_TO_SECOND ,
			 T_INTERVAL_SECOND           :

			uMinUnscaledByteCount, uMaxUnscaledByteCount, bScaled, uMinScaledByteCount, uMaxScaledByteCount := timeRanges (uCookedDataType)

			switch {

				case teraval.ByteCount () >= uMinUnscaledByteCount && teraval.ByteCount () <= uMaxUnscaledByteCount: // do nothing

				case bScaled && teraval.ByteCount () >= uMinScaledByteCount && teraval.ByteCount () <= uMaxScaledByteCount: // do nothing

				default:
					err = rows.m_stmt.m_con.makeDriverError (nil, 0,
						fmt.Sprintf ("Parameter %v SQL data type %v %v invalid length %v",
							iParam + 1,
							sTypeName,
							uCookedDataType,
							teraval.ByteCount ()))

					return
			}
	} // end switch uCookedDataType

	if rows.m_stmt.m_con.hasDebugLog () {
		debugLog (fmt.Sprintf ("validateTeradataValue: iParam=%v uCookedDataType=%v sTypeName=%v ByteCount=%v", iParam, uCookedDataType, sTypeName, teraval.ByteCount ()))
	}

	return
}  // end validateTeradataValue

func (rows *TeradataRows) setBindMetadata (teraval TeradataValue, bmd *bindMetadata, iBindRow int, iParam int, escapeParamTypeMetadata escParamDataTypeMetadata) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter setBindMetadata %v %v %v", rows, bmd, escapeParamTypeMetadata))
		defer func () { traceLog (fmt.Sprintf ("< leave setBindMetadata %v %v", rows, bmd)) } ()
	}

	uByteCount := teraval.ByteCount ()

	if bmd.m_uCookedDataType == 0 {

		bmd.m_uCookedDataType   = teraval.CookedDataType ()
		bmd.m_uNullableDataType = bmd.m_uCookedDataType | 1
		bmd.m_uByteCount        = uByteCount
	}

	// JDBC RFC 146722 Improve DBS statement cache hit ratio for PreparedStatement VARCHAR bind values.
	// The DBS uses the combination of SQL request text and metadata request parcel as the DBS statement cache key. Differing maximum VARCHAR lengths in metadata request parcels
	// will cause DBS statement cache misses. The code below attempts to produce a cache hit. If the parameter marker is a direct VARCHAR column reference, and the application bound
	// one or more VARCHAR values, and the maximum bound VARCHAR length is less than 32000 bytes, then raise the value to 32000 instead, in order to provide more similar and consistent
	// metadata request parcels across multiple execute requests. The value 32000 was chosen because it is the largest value that works with all supported session character sets.

	var uPrecision        uint8
	var uScale            uint8
	var uIntervalDigits   uint16

	switch bmd.m_uCookedDataType {

		case T_DECIMAL:

			uPrecision = decimalPrecisionFromByteCount (uByteCount)
			uScale     = teraval.Scale ()

		case T_NUMBER:

			uByteCount = 19 // variable-length data type - maximum byte count for this data type
			uPrecision = 0x80
			uScale     = 0x80

		case T_TIME                       ,
			 T_TIMESTAMP                  ,
			 T_TIME_WITH_TIME_ZONE        ,
			 T_TIMESTAMP_WITH_TIME_ZONE   ,
			 T_PERIOD_DATE                ,
			 T_PERIOD_TIME                ,
			 T_PERIOD_TIME_WITH_TIME_ZONE :

			uPrecision = uint8 (uByteCount)
			uScale = func () uint8 { if teraval.Scale () < escapeParamTypeMetadata.m_uScale { return escapeParamTypeMetadata.m_uScale } ; return teraval.Scale () } ()

		case T_PERIOD_TIMESTAMP:

			uByteCount = 20 // variable-length data type - null values have zero length, non-null values have this length
			uScale = func () uint8 { if teraval.Scale () < escapeParamTypeMetadata.m_uScale { return escapeParamTypeMetadata.m_uScale } ; return teraval.Scale () } ()
			uPrecision = uint8 (uByteCount)

		case T_PERIOD_TIMESTAMP_WITH_TIME_ZONE:

			uByteCount = 24 // variable-length data type - null values have zero length, non-null values have this length
			uScale = func () uint8 { if teraval.Scale () < escapeParamTypeMetadata.m_uScale { return escapeParamTypeMetadata.m_uScale } ; return teraval.Scale () } ()
			uPrecision = uint8 (uByteCount)

		case T_INTERVAL_YEAR             ,
			 T_INTERVAL_YEAR_TO_MONTH    ,
			 T_INTERVAL_MONTH            ,
			 T_INTERVAL_DAY              ,
			 T_INTERVAL_DAY_TO_HOUR      ,
			 T_INTERVAL_DAY_TO_MINUTE    ,
			 T_INTERVAL_HOUR             ,
			 T_INTERVAL_HOUR_TO_MINUTE   ,
			 T_INTERVAL_MINUTE           ,
			 T_INTERVAL_DAY_TO_SECOND    ,
			 T_INTERVAL_HOUR_TO_SECOND   ,
			 T_INTERVAL_MINUTE_TO_SECOND ,
			 T_INTERVAL_SECOND           :

			if rows.m_bSendSIP {

				uScale = teraval.Scale ()
				uMinUnscaledByteCount, _, _, uMinScaledByteCount, _ := timeRanges (bmd.m_uCookedDataType)
				if uScale > 0 {
					uIntervalDigits = uint16 (uByteCount - uMinScaledByteCount + 2 - uint64 (uScale))
				} else {
					uIntervalDigits = uint16 (uByteCount - uMinUnscaledByteCount + 1)
				}

				uScale          = func () uint8  { if uScale < escapeParamTypeMetadata.m_uScale { return escapeParamTypeMetadata.m_uScale } ; return uScale } ()
				uIntervalDigits = func () uint16 { if uIntervalDigits < escapeParamTypeMetadata.m_uIntervalDigits { return escapeParamTypeMetadata.m_uIntervalDigits } ; return uIntervalDigits } ()

			} // end if

		case T_JSON_AS_DEFERRED ,
			 T_JSON_AS_LOCATOR  ,
			 T_JSON             :
				uByteCount = 16776192

		case T_BLOB_AS_DEFERRED ,
			 T_CLOB_AS_DEFERRED ,
			 T_XML_AS_DEFERRED  :
				uByteCount = 2097088000

		case T_VARCHAR,
			 T_VARBYTE:
			if uByteCount < escapeParamTypeMetadata.m_uByteCount { uByteCount = escapeParamTypeMetadata.m_uByteCount }

		case T_LONG_VARCHAR:
			if uByteCount < 32000 { uByteCount = 32000 }

		case T_LONG_VARBYTE:
			if uByteCount < 64000 { uByteCount = 64000 }

	} // end switch

	if uScale > bmd.m_uScale {
		bmd.m_uScale = uScale
	}

	if uByteCount > bmd.m_uByteCount {
		bmd.m_uByteCount = uByteCount
	}

	if uPrecision > bmd.m_uPrecision {
		bmd.m_uPrecision = uPrecision
	}

	if uIntervalDigits > bmd.m_uIntervalDigits {
		bmd.m_uIntervalDigits = uIntervalDigits
	}

	return

} // end setBindMetadata

func (rows *TeradataRows) composeStatementInfoParcel (aBindMetadata [] bindMetadata) [] byte {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter composeStatementInfoParcel %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave composeStatementInfoParcel %v", rows)) } ()
	}

	var aabyBodyChunks [][] byte

	for _, bmd := range aBindMetadata {

		bUseStructLayout := bmd.m_uServerDataType > 0

		aabyBodyChunks = append (aabyBodyChunks,
			twoBytes   (func () uint16 { if bUseStructLayout { return LAYOUT_TYPE_STRUCT_CONTENT } ; return LAYOUT_TYPE_LIMITED_CONTENT } ()), // item layout type
			twoBytes   (/*ParameterValue*/   8), // item content type
			twoBytes   (/*Item Body Length*/ func () uint16 { if bUseStructLayout { return 24 } ; return 16 } ()),
			twoBytes   (bmd.m_uNullableDataType),
			eightBytes (bmd.m_uByteCount),
			twoBytes   (uint16 (bmd.m_uPrecision)),
			twoBytes   (bmd.m_uIntervalDigits),
			twoBytes   (uint16 (bmd.m_uScale)))

		if bUseStructLayout {
			aabyBodyChunks = append (aabyBodyChunks,
				twoBytes (0), // struct depth
				twoBytes (0), // UDT name length
				twoBytes (0), // attribute name length
				// Add attribute name here if name length greater than zero
				twoBytes (bmd.m_uServerDataType))
		}
	} // end for

	aabyBodyChunks = append (aabyBodyChunks,
		twoBytes (/*EndOfList     */ 4), // item layout type
		twoBytes (/*ParameterValue*/ 8), // item content type
		twoBytes (                   0)) // item body length

	return makeParcel (PCLSTATEMENTINFO, aabyBodyChunks ...)

} // end method composeStatementInfoParcel

func (rows *TeradataRows) composeDataInfoParcel (aBindMetadata [] bindMetadata) [] byte {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter composeDataInfoParcel %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave composeDataInfoParcel %v", rows)) } ()
	}

	uFlavor := func () uint16 { if rows.m_bUseLOB { return PCLDATAINFOX } ; return PCLDATAINFO } ()

	var aabyBodyChunks [][] byte

	if uFlavor == PCLDATAINFO {
		aabyBodyChunks = append (aabyBodyChunks, twoBytes (uint16 (len (aBindMetadata)))) // previously validated param count <= 0xFFFF
	} else { // PCLDATAINFOX
		aabyBodyChunks = append (aabyBodyChunks, fourBytes (uint32 (len (aBindMetadata))))
	}

	for _, bmd := range aBindMetadata {

		aabyBodyChunks = append (aabyBodyChunks, twoBytes (bmd.m_uNullableDataType))

		switch bmd.m_uCookedDataType {

			case T_DECIMAL                         ,
				 T_NUMBER                          ,
				 T_PERIOD_DATE                     ,
				 T_PERIOD_TIME                     ,
				 T_PERIOD_TIME_WITH_TIME_ZONE      ,
				 T_PERIOD_TIMESTAMP                ,
				 T_PERIOD_TIMESTAMP_WITH_TIME_ZONE :

				if uFlavor == PCLDATAINFO {
					aabyBodyChunks = append (aabyBodyChunks, [] byte { bmd.m_uPrecision, bmd.m_uScale })
				} else { // PCLDATAINFOX
					aabyBodyChunks = append (aabyBodyChunks, fourBytes (uint32 (bmd.m_uPrecision)), fourBytes (uint32 (bmd.m_uScale)))
				}

			default:

				if uFlavor == PCLDATAINFO {
					aabyBodyChunks = append (aabyBodyChunks, twoBytes (uint16 (bmd.m_uByteCount))) // previously validated length <= 0xFFFF
				} else { // PCLDATAINFOX
					aabyBodyChunks = append (aabyBodyChunks, eightBytes (bmd.m_uByteCount))
				}

		} // end switch

	} // end for

	return makeParcel (uFlavor, aabyBodyChunks ...)

} // end method composeDataInfoParcel

func (rows *TeradataRows) composeDataParcel (aBindValues [] driver.NamedValue) [] byte {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter composeDataParcel %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave composeDataParcel %v", rows)) } ()
	}

	uFlavor := func () uint16 { if rows.m_bUseLOB { return PCLMULTIPARTINDICDATA } ; return PCLINDICDATA } ()

	nNullIndicatorByteCount := (len (aBindValues) - 1) / 8 + 1
	abyNullIndicators       := make ([] byte, nNullIndicatorByteCount)
	aabyBodyChunks          := [][] byte { abyNullIndicators }

	for iParam, namedval := range aBindValues {

		teraval         := namedval.Value.(TeradataValue)
		uCookedDataType := teraval.CookedDataType ()

		nPrefixByteCount, _, _, _, _ := dataTypeInfo (uCookedDataType)

		if teraval.m_bPresent {

			switch nPrefixByteCount {

				case 1: aabyBodyChunks = append (aabyBodyChunks, [] byte { uint8  (len (teraval.m_abyValue)) }) // previously validated length <= 0xFF
				case 2: aabyBodyChunks = append (aabyBodyChunks, twoBytes (uint16 (len (teraval.m_abyValue)))) // previously validated length <= 0xFFFF
				case 8: aabyBodyChunks = append (aabyBodyChunks, eightBytes (uint64 (len (teraval.m_abyValue)))) // previously validated length <= 0xFFFFFFFF
			}

			aabyBodyChunks = append (aabyBodyChunks, teraval.m_abyValue)

		} else { // null value

			abyNullIndicators [iParam / 8] |= (0x80 >> (uint (iParam) % 8))

			switch nPrefixByteCount {

				case 0 : aabyBodyChunks = append (aabyBodyChunks, make ([] byte, teraval.m_uByteCount))
				default: aabyBodyChunks = append (aabyBodyChunks, make ([] byte, nPrefixByteCount    ))
			}
		}
	} // end for

	return makeParcel (uFlavor, aabyBodyChunks ...)

} // end method composeDataParcel

func (rows *TeradataRows) processEndRequest (aabyParcelHeaders [][] byte) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter processEndRequest %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave processEndRequest %v", rows)) } ()
	}

	bPartitionDBCCONS := strings.EqualFold (rows.m_stmt.m_con.m_params.M_sPartition, "DBCCONS")

	if bPartitionDBCCONS { return } // PCLENDREQUEST is skipped for the DBCCONS partition

	if rows.keepResponse () { return } // PCLENDREQUEST is not relevant when we are using PCLBIGKEEPRESP for LOB locator support

	for _, abyParcelHeader := range aabyParcelHeaders {

		if cookedParcelFlavor (abyParcelHeader) == PCLENDREQUEST {
			rows.m_bFreedSpool = true
			break
		}
	}
} // end method processEndRequest

func (rows *TeradataRows) processResponseBundle (errCause error, aabyParcelHeaders [][] byte) (err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter processResponseBundle %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave processResponseBundle %v", rows)) } ()
	}

	bPartitionDBCCONS    := strings.EqualFold (rows.m_stmt.m_con.m_params.M_sPartition, "DBCCONS")
	bPartitionFastExport := strings.EqualFold (rows.m_stmt.m_con.m_params.M_sPartition, "EXPORT")

	if ! bPartitionFastExport {
		rows.m_aParamMetadata    = nil
		rows.m_aColumnMetadata   = nil
		rows.m_asColumnNames     = nil
		rows.m_atColumnScanTypes = nil
	}

	if rows.m_bFakeResultSets { rows.m_nFakeState = returnRow }

	if rows.m_bProcessingBatchRequest && containsStatementError (aabyParcelHeaders) {
		err = rows.gatherBatchStatementErrors (aabyParcelHeaders)
		return
	}

	uFlavor0 := peekCookedParcelFlavor (0, aabyParcelHeaders)

	switch uFlavor0 {

		case PCLSTMTSTATUS:

			rows.m_uStatementNumber,
			rows.m_uActivityType   ,
			rows.m_uActivityCount  ,
			rows.m_uWarningCode    ,
			rows.m_sRawWarningMsg  ,
			rows.m_sWarningMsg     ,
			err                    = rows.m_stmt.m_con.processStatementStatusParcel (aabyParcelHeaders [0])

		case PCLSUCCESS:

			rows.m_uStatementNumber,
			rows.m_uActivityType   ,
			rows.m_uActivityCount  ,
			rows.m_uWarningCode    ,
			rows.m_sRawWarningMsg  ,
			rows.m_sWarningMsg     = rows.m_stmt.m_con.processSuccessParcel (aabyParcelHeaders [0])

		case PCLFAILURE, PCLERROR:

			uErrorCode, uErrorMsg, uStatementNumber := rows.m_stmt.m_con.retrieveErrorParcelDetails (aabyParcelHeaders [0])

			if bPartitionDBCCONS {
				rows.m_stmt.m_con.m_bPromptMode = false
			}

			if rows.m_bProcessingBatchRequest {
				rows.m_batchStatementErrorMap [uStatementNumber + rows.m_uBatchSegmentStartRow] = batchStatementError {uErrorCode, uErrorMsg}
			}

			rows.m_uWarningCode  ,
			rows.m_sRawWarningMsg,
			rows.m_sWarningMsg   ,
			err                  = rows.m_stmt.m_con.processErrorParcel (errCause, aabyParcelHeaders [0])

			rows.m_bFreedSpool = true
			return

		default:

			err = rows.m_stmt.m_con.makeDriverError (nil, 0,
				fmt.Sprintf ("The Teradata Database provided unexpected response parcel %v %v",
					parcelFlavor (uFlavor0),
					uFlavor0))

			return

	} // end switch uParcelFlavor

	if peekCookedParcelFlavor (1, aabyParcelHeaders) == PCLRESULTSET {
		aabyParcelHeaders = aabyParcelHeaders [1 : ]
	}

	uFlavor1 := peekCookedParcelFlavor (1, aabyParcelHeaders)
	uFlavor2 := peekCookedParcelFlavor (2, aabyParcelHeaders)

	switch {

		case uFlavor1 == PCLENDREQUEST:
			return

		case uFlavor1 == PCLENDSTATEMENT:

			rows.m_abyCurrentParcelHeader = aabyParcelHeaders [1]
			return

		case uFlavor1 == PCLDATAINFO || uFlavor1 == PCLDATAINFOX:

			err = rows.parseDataInfoParcel (uFlavor1, aabyParcelHeaders [1])
			if err != nil { return }

			rows.m_abyCurrentParcelHeader = aabyParcelHeaders [1]

		case uFlavor1 == PCLPREPINFO || uFlavor1 == PCLPREPINFOX:

			err = rows.parsePrepInfoParcel (uFlavor1, aabyParcelHeaders [1])
			if err != nil { return }

			switch uFlavor2 {
				case PCLDATAINFO, PCLDATAINFOX : rows.m_abyCurrentParcelHeader = aabyParcelHeaders [2] // skip PCLDATAINFO after PCLPREPINFO
				default                        : rows.m_abyCurrentParcelHeader = aabyParcelHeaders [1]
			}

		case uFlavor1 == PCLSTATEMENTINFO && uFlavor2 == PCLSTATEMENTINFOEND:

			err = rows.parseStatementInfoParcel (aabyParcelHeaders [1])
			if err != nil { return }

			rows.m_abyCurrentParcelHeader = aabyParcelHeaders [2]

		case bPartitionDBCCONS:

			rows.setSingleVarcharColumnMetadata ()

			rows.m_abyCurrentParcelHeader = aabyParcelHeaders [0]

		case bPartitionFastExport:

			rows.m_abyCurrentParcelHeader = aabyParcelHeaders [0]

		default:

			err = rows.m_stmt.m_con.makeDriverError (nil, 0,
				fmt.Sprintf ("The Teradata Database provided unexpected metadata parcel %v %v",
					parcelFlavor (uFlavor1),
					uFlavor1))

			return

	} // end switch

	err = rows.fetchNextParcel ()
	if err != nil { return }

	return

} // end method processResponseBundle

func containsStatementError (aabyParcelHeaders [][] byte) bool {

	for _, abyParcelHeader := range aabyParcelHeaders {
		if cookedParcelFlavor (abyParcelHeader) == PCLSTMTERROR { return true }
	}

	return false

} // end containsStatementError

func (rows *TeradataRows) gatherBatchStatementErrors (aabyParcelHeaders [][] byte) (err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter gatherBatchStatementErrors %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave gatherBatchStatementErrors %v", rows)) } ()
	}

	for i, abyParcelHeader := range aabyParcelHeaders {

		switch cookedParcelFlavor (abyParcelHeader) {

			case PCLSTMTSTATUS:

				// Process only one Statement Status parcel of the batch and skip the rest
				if rows.m_uStatementNumber == 0 {

					rows.m_uStatementNumber,
					rows.m_uActivityType   ,
					rows.m_uActivityCount  ,
					rows.m_uWarningCode    ,
					rows.m_sRawWarningMsg  ,
					rows.m_sWarningMsg     ,
					err                    = rows.m_stmt.m_con.processStatementStatusParcel (aabyParcelHeaders [i])
				}

			case PCLSUCCESS :

				// Process only one Success parcel of the batch and skip the rest
				if rows.m_uStatementNumber == 0 {

					rows.m_uStatementNumber,
					rows.m_uActivityType   ,
					rows.m_uActivityCount  ,
					rows.m_uWarningCode    ,
					rows.m_sRawWarningMsg  ,
					rows.m_sWarningMsg     = rows.m_stmt.m_con.processSuccessParcel (aabyParcelHeaders [i])
				}

			case PCLSTMTERROR :

				uErrorCode, sErrorMsg, uStatementNumber := rows.m_stmt.m_con.retrieveErrorParcelDetails (abyParcelHeader)
				rows.m_batchStatementErrorMap [uStatementNumber + rows.m_uBatchSegmentStartRow] = batchStatementError {uErrorCode, sErrorMsg}

		} // end switch
	} // end for

	return

}  // end gatherBatchStatementErrors

func (rows *TeradataRows) appendLineToWarningMsg (s string) {

	s = strings.TrimSpace (s)
	if s != "" {
		if len (rows.m_sWarningMsg) > 0 {
			rows.m_sWarningMsg += "\n"
		}

		rows.m_sWarningMsg += s
	}
} // end method appendLineToWarningMsg

func (rows *TeradataRows) fetchMessagesFromResultSet () error {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter fetchMessagesFromResultSet %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave fetchMessagesFromResultSet %v", rows)) } ()
	}

	bIsMessagesResultSet := messagesResultSet (rows.m_uActivityType) && len (rows.m_asColumnNames) == 1

	if rows.m_stmt.m_con.hasDebugLog () {
		debugLog (fmt.Sprintf ("fetchMessagesFromResultSet: %v bIsMessagesResultSet=%v", rows, bIsMessagesResultSet))
	}

	if ! bIsMessagesResultSet { return nil }

	nSavedFakeState := rows.m_nFakeState
	defer func () { rows.m_nFakeState = nSavedFakeState } ()
	rows.m_nFakeState = turnedOff

	rows.appendLineToWarningMsg (rows.m_asColumnNames [0]) // column title before all column values

	dest := make ([] driver.Value, 1)

	for {
		err := rows.Next (dest)
		if err == nil {
			rows.appendLineToWarningMsg (fmt.Sprintf ("%v", dest [0])) // one compile message per line

		} else if errors.Is (err, io.EOF) { // we fetched all rows from the result set

			if rows.m_stmt.m_con.hasDebugLog () {
				debugLog (fmt.Sprintf ("fetchMessagesFromResultSet: %v code=%v msg=%v", rows, rows.m_uWarningCode, rows.m_sWarningMsg))
			}

			return nil

		} else {
			return err // bubble up any other errors
		}
	} // end for

} // end method fetchMessagesFromResultSet

func (rows *TeradataRows) processElicitDataParcel (ctx context.Context, abyParcelHeader [] byte) (aabyOutputParcelHeaders [][] byte, errFile error, errCon error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter processElicitDataParcel %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave processElicitDataParcel %v", rows)) } ()
	}

	uParcelFlavor     := cookedParcelFlavor (abyParcelHeader)
	abyParcelBody     := constrainedParcelBody (abyParcelHeader)

	nDeferredLobToken := int32 (consumeInt (&abyParcelBody))

	abyDeferredLob    := rows.m_stmt.m_con.unRegisterDeferredLob (nDeferredLobToken)
	uDeferredLobLen   := uint64 (len (abyDeferredLob))

	abyBuffer  := make ([] byte, minUint64 (uDeferredLobLen, uint64 (rows.m_stmt.m_con.m_config.M_uMaxUsingDataSizeInBytes - 100)))
	uBufferLen := uint64 (len (abyBuffer))

	if rows.m_stmt.m_con.hasDebugLog () {
		debugLog (fmt.Sprintf ("processElicitFileData: %v %v uStatementNumber=%v nDeferredLobToken=%v uDeferredLobLen=%v, uBufferLen=%v",
			rows.m_stmt.m_con, parcelFlavor (uParcelFlavor), rows.m_uStatementNumber, nDeferredLobToken, uDeferredLobLen, uBufferLen))
	}

	var uStartPosition, uEndPosition uint64

	// LOB length is only sent in first MultiPartData parcel and should be set to 0 since the server ignores this number
	abyZeroLobLength := make ([] byte, 8)

	for bEOF := false ; ! bEOF ; {

		if wasInterrupted (ctx) {
			rows.m_stmt.m_con.executeAsynchronousAbort (true /*bUseUCAbort*/)
			rows.m_bAbortSent = true
			errCon = rows.m_stmt.m_con.makeInterruptError (ctx.Err (), true /*abort sent*/)
			return
		}

		if uStartPosition + uBufferLen >= uDeferredLobLen {
			uEndPosition = uDeferredLobLen
			bEOF = true

		} else {
			uEndPosition = uStartPosition + uBufferLen
		}

		aabyOutputParcelHeaders, errCon = rows.sendElicitedChunk (abyDeferredLob [ uStartPosition : uEndPosition], abyZeroLobLength, bEOF)
		if errCon != nil { return }

		uStartPosition = uEndPosition
		abyZeroLobLength = nil

		if ! bEOF && peekCookedParcelFlavor (0, aabyOutputParcelHeaders) != PCLELICITDATARECEIVED {
			errCon = rows.m_stmt.m_con.makeDriverError (nil, 0, "The Teradata Database did not provide expected Received Elicit Data parcel")
			return
		}
	} // end for

	return

} // end method processElicitDataParcel

func (rows *TeradataRows) processElicitFileParcel (ctx context.Context, abyParcelHeader [] byte) (aabyOutputParcelHeaders [][] byte, errFile error, errCon error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter processElicitFileParcel %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave processElicitFileParcel %v", rows)) } ()
	}

	uParcelFlavor    := cookedParcelFlavor (abyParcelHeader)
	abyParcelBody    := constrainedParcelBody (abyParcelHeader)

	uLanguage        := consumeByte (&abyParcelBody)
	uFileType        := consumeByte (&abyParcelBody)
	uFileSupplied    := consumeByte (&abyParcelBody)
	/* discard pad */   consumeByte (&abyParcelBody)
	sFileName        := string (rows.m_stmt.m_con.consumeVarchar (&abyParcelBody))

	if rows.m_stmt.m_con.hasDebugLog () {
		debugLog (fmt.Sprintf ("processElicitFileParcel: %v %v uStatementNumber=%v uLanguage=%v uFileType=%v uFileSupplied=%v sFileName=%v", rows.m_stmt.m_con, parcelFlavor (uParcelFlavor), rows.m_uStatementNumber, uLanguage, uFileType, uFileSupplied, sFileName))
	}

	var f *os.File
	f, errFile = os.Open (sFileName)

	if errFile != nil {

		errFile = rows.m_stmt.m_con.makeDriverError (errFile, 0, fmt.Sprintf ("File %v was requested by the Teradata Database and could not be opened", sFileName))

		// If there is an error reading the file, the driver must send zero-length data to the
		// database, which should cause the database to return a 5602 error and abort the request.
		aabyOutputParcelHeaders, errCon = rows.sendElicitedFileChunk ([] byte {}, /*bEOF*/ true)
		return
	}

	defer f.Close ()

	var fi os.FileInfo
	fi, errFile = f.Stat ()

	if errFile != nil {

		errFile = rows.m_stmt.m_con.makeDriverError (errFile, 0, fmt.Sprintf ("Could not obtain information for file %v", sFileName))

		// If there is an error reading the file, the driver must send zero-length data to the
		// database, which should cause the database to return a 5602 error and abort the request.
		aabyOutputParcelHeaders, errCon = rows.sendElicitedFileChunk ([] byte {}, /*bEOF*/ true)
		return
	}

	uBytesToRead := uint64 (fi.Size ())
	abyBuffer := make ([] byte, minUint64 (uBytesToRead, uint64 (rows.m_stmt.m_con.m_config.M_uMaxRequestSizeInBytes - 100)))

	for bEOF := false ; ! bEOF ; {

		if wasInterrupted (ctx) {
			rows.m_stmt.m_con.executeAsynchronousAbort (true /*bUseUCAbort*/)
			rows.m_bAbortSent = true
			errCon = rows.m_stmt.m_con.makeInterruptError (ctx.Err (), true /*abort sent*/)
			return
		}

		var nAmountRead int
		nAmountRead, errFile = f.Read (abyBuffer)

		if errFile != nil {

			errFile = rows.m_stmt.m_con.makeDriverError (errFile, 0, fmt.Sprintf ("Failure reading from resource %v", sFileName))

			// If there is an error reading the file, the driver must send zero-length data to the
			// database, which should cause the database to return a 5602 error and abort the request.
			aabyOutputParcelHeaders, errCon = rows.sendElicitedFileChunk ([] byte {}, /*bEOF*/ true)
			return
		}

		uBytesToRead -= uint64 (nAmountRead)
		bEOF = uBytesToRead <= 0

		aabyOutputParcelHeaders, errCon = rows.sendElicitedFileChunk (abyBuffer [ : nAmountRead], bEOF)
		if errCon != nil { return }

	} // end for

	return

} // end method processElicitFileParcel

func (rows *TeradataRows) sendElicitedFileChunk (abyBuffer [] byte, bEOF bool) (aabyOutputParcelHeaders [][] byte, err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter sendElicitedFileChunk %v len=%v eof=%v", rows, len (abyBuffer), bEOF))
		defer func () { traceLog (fmt.Sprintf ("< leave sendElicitedFileChunk %v len=%v eof=%v", rows, len (abyBuffer), bEOF)) } ()
	}

	return rows.sendElicitedChunk (abyBuffer, /*abyElicitDataLength*/ nil, bEOF)

} // end method sendElicitedFileChunk

func (rows *TeradataRows) sendElicitedChunk (abyBuffer [] byte, abyElicitDataLength [] byte, bEOF bool) (aabyOutputParcelHeaders [][] byte, err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter sendElicitedChunk %v len=%v eof=%v", rows, len (abyBuffer), bEOF))
		defer func () { traceLog (fmt.Sprintf ("< leave sendElicitedChunk %v len=%v eof=%v", rows, len (abyBuffer), bEOF)) } ()
	}

	var aabyElicitParcels [][] byte

	aabyElicitParcels = append (aabyElicitParcels, makeParcel (PCLMULTIPARTDATA, abyElicitDataLength, abyBuffer))

	if bEOF {
		aabyElicitParcels = append (aabyElicitParcels, makeParcel (PCLENDMULTIPARTDATA))
	}

	// Compose a response message because the driver is responding to the Elicit Data request from the database.
	abyElicitMsg := rows.m_stmt.m_con.makeResponseMessage (MESSAGE_KIND_ELICIT, NEW_CRYPTO_NONCE, rows.m_uRequestNumber, aabyElicitParcels ...)

	var abyElicitResponseMsg [] byte
	abyElicitResponseMsg, _, err = rows.m_stmt.m_con.sendAndReceive (&NetworkIO { abyMsgToSend: abyElicitMsg, rows: rows })
	if err != nil { return }

	aabyOutputParcelHeaders = getParcelHeadersFromMessage (abyElicitResponseMsg)

	if rows.m_stmt.m_con.hasDebugLog () { debugLogParcelHeaders ("sendElicitedChunk", aabyOutputParcelHeaders) }
	return

} // end method sendElicitedChunk

func (rows *TeradataRows) setSingleVarcharColumnMetadata () {

	col := columnMetadata { M_uCookedDataType: T_VARCHAR }

	rows.m_aColumnMetadata   = append (rows.m_aColumnMetadata   , col)
	rows.m_asColumnNames     = append (rows.m_asColumnNames     , "")
	rows.m_atColumnScanTypes = append (rows.m_atColumnScanTypes , outboundType (rows.m_bTeradataValues, col.M_uCookedDataType))

} // end method setSingleVarcharColumnMetadata

func (rows *TeradataRows) parseDataInfoParcel (uFlavor uint16, abyParcelHeader [] byte) (err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter parseDataInfoParcel %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave parseDataInfoParcel %v", rows)) } ()
	}

	abyParcelBody := constrainedParcelBody (abyParcelHeader)

	var nColumnCount int
	if uFlavor == PCLDATAINFO {
		nColumnCount = int (consumeShort (&abyParcelBody))
	} else { // PCLDATAINFOX
		nColumnCount = int (consumeInt (&abyParcelBody))
	}

	for nColumn := 1 ; nColumn <= nColumnCount ; nColumn++ {

		col                   := columnMetadata {}
		col.M_uRawDataType     = consumeShort (&abyParcelBody)
		col.M_uCookedDataType  = cookedDataType (col.M_uRawDataType)

		if uFlavor == PCLDATAINFO {

			switch col.M_uCookedDataType {

				case T_DECIMAL:

					col.M_uPrecision   = abyParcelBody [0]
					col.M_uScale       = abyParcelBody [1]
					col.M_uByteCount   = byteCountFromDecimalPrecision (col.M_uPrecision)

				case T_PERIOD_DATE                     ,
					 T_PERIOD_TIME                     ,
					 T_PERIOD_TIME_WITH_TIME_ZONE      ,
					 T_PERIOD_TIMESTAMP                ,
					 T_PERIOD_TIMESTAMP_WITH_TIME_ZONE :

					col.M_uByteCount   = uint64 (abyParcelBody [0])
					col.M_uScale       = abyParcelBody [1]

				default:

					col.M_uByteCount   = uint64 (binary.BigEndian.Uint16 (abyParcelBody))

			} // end switch

			skipShort (&abyParcelBody)

		} else { // PCLDATAINFOX

			switch col.M_uCookedDataType {

				case T_DECIMAL:

					col.M_uPrecision   = abyParcelBody [3]
					col.M_uScale       = abyParcelBody [7]
					col.M_uByteCount   = byteCountFromDecimalPrecision (col.M_uPrecision)

				case T_PERIOD_DATE                     ,
					 T_PERIOD_TIME                     ,
					 T_PERIOD_TIME_WITH_TIME_ZONE      ,
					 T_PERIOD_TIMESTAMP                ,
					 T_PERIOD_TIMESTAMP_WITH_TIME_ZONE :

					col.M_uByteCount   = uint64 (abyParcelBody [3])
					col.M_uScale       = abyParcelBody [7]

				default:

					col.M_uByteCount   = binary.BigEndian.Uint64 (abyParcelBody)

			} // end switch

			skipLong (&abyParcelBody)

		} // end if uFlavor

		if rows.m_stmt.m_con.hasDebugLog () { debugLog (fmt.Sprintf ("parseDataInfoParcel: %v nColumn=%v %+v", parcelFlavor (uFlavor), nColumn, col)) }

		_, col.m_bHasVarLength, _, _, col.M_sTypeName = dataTypeInfo (col.M_uCookedDataType)

		rows.m_aColumnMetadata   = append (rows.m_aColumnMetadata   , col)
		rows.m_asColumnNames     = append (rows.m_asColumnNames     , "")
		rows.m_atColumnScanTypes = append (rows.m_atColumnScanTypes , outboundType (rows.m_bTeradataValues, col.M_uCookedDataType))

	} // end for nColumn

	return

} // end method parseDataInfoParcel

func (rows *TeradataRows) parsePrepInfoParcel (uFlavor uint16, abyParcelHeader [] byte) (err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter parsePrepInfoParcel %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave parsePrepInfoParcel %v", rows)) } ()
	}

	abyParcelBody := constrainedParcelBody (abyParcelHeader)

	uSummaryGroupCount := binary.BigEndian.Uint16 (abyParcelBody [8 : ])
	if uSummaryGroupCount != 0 {
		err = rows.m_stmt.m_con.makeDriverError (nil, 0, "Summary items are not supported")
		return
	}

	nColumnCount := int (binary.BigEndian.Uint16 (abyParcelBody [10 : ]))

	abyPrepInfoItem := abyParcelBody [12 : ]

	for nColumn := 1 ; nColumn <= nColumnCount ; nColumn++ {

		col                   := columnMetadata {}
		col.M_uRawDataType     = consumeShort (&abyPrepInfoItem)
		col.M_uCookedDataType  = cookedDataType (col.M_uRawDataType)
		bInParam              := isInParam (col.M_uRawDataType)

		if uFlavor == PCLPREPINFO {

			switch col.M_uCookedDataType {

				case T_DECIMAL:

					col.M_uPrecision   = abyPrepInfoItem [0]
					col.M_uScale       = abyPrepInfoItem [1]
					col.M_uByteCount   = byteCountFromDecimalPrecision (col.M_uPrecision)

				case T_PERIOD_DATE                     ,
					 T_PERIOD_TIME                     ,
					 T_PERIOD_TIME_WITH_TIME_ZONE      ,
					 T_PERIOD_TIMESTAMP                ,
					 T_PERIOD_TIMESTAMP_WITH_TIME_ZONE :

					col.M_uByteCount   = uint64 (abyPrepInfoItem [0])
					col.M_uScale       = abyPrepInfoItem [1]

				default:

					col.M_uByteCount   = uint64 (binary.BigEndian.Uint16 (abyPrepInfoItem))

			} // end switch

			skipShort (&abyPrepInfoItem)

		} else { // PCLPREPINFOX

			switch col.M_uCookedDataType {

				case T_DECIMAL:

					col.M_uPrecision   = abyPrepInfoItem [3]
					col.M_uScale       = abyPrepInfoItem [7]
					col.M_uByteCount   = byteCountFromDecimalPrecision (col.M_uPrecision)

				case T_PERIOD_DATE                     ,
					 T_PERIOD_TIME                     ,
					 T_PERIOD_TIME_WITH_TIME_ZONE      ,
					 T_PERIOD_TIMESTAMP                ,
					 T_PERIOD_TIMESTAMP_WITH_TIME_ZONE :

					col.M_uByteCount   = uint64 (abyPrepInfoItem [3])
					col.M_uScale       = abyPrepInfoItem [7]

				default:

					col.M_uByteCount   = binary.BigEndian.Uint64 (abyPrepInfoItem)

			} // end switch

			skipLong (&abyPrepInfoItem)

			col.M_uServerCharsetCode = consumeByte (&abyPrepInfoItem)
			col.M_bCaseSensitive     = (consumeByte (&abyPrepInfoItem) & 0x80) != 0

		} // end if uFlavor

		col.M_sName   = string (rows.m_stmt.m_con.consumeVarchar (&abyPrepInfoItem))
		col.M_sFormat = string (rows.m_stmt.m_con.consumeVarchar (&abyPrepInfoItem))
		col.M_sTitle  = string (rows.m_stmt.m_con.consumeVarchar (&abyPrepInfoItem))

		if rows.m_stmt.m_con.hasDebugLog () { debugLog (fmt.Sprintf ("parsePrepInfoParcel: %v nColumn=%v %+v", parcelFlavor (uFlavor), nColumn, col)) }

		if ! bInParam {

			_, col.m_bHasVarLength, _, _, col.M_sTypeName = dataTypeInfo (col.M_uCookedDataType)

			rows.m_aColumnMetadata   = append (rows.m_aColumnMetadata   , col)
			rows.m_asColumnNames     = append (rows.m_asColumnNames     , col.M_sName)
			rows.m_atColumnScanTypes = append (rows.m_atColumnScanTypes , outboundType (rows.m_bTeradataValues, col.M_uCookedDataType))

		} // end if ! bInParam

	} // end for nColumn

	return

} // end method parsePrepInfoParcel

func (rows *TeradataRows) parseStatementInfoParcel (abyParcelHeader [] byte) (err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter parseStatementInfoParcel %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave parseStatementInfoParcel %v", rows)) } ()
	}

	abyParcelBody := constrainedParcelBody (abyParcelHeader)

	var uPrevItemContentType uint16
	var nColumn int

	for len (abyParcelBody) > 0 {

		uItemLayoutType  := consumeShort (&abyParcelBody)
		uItemContentType := consumeShort (&abyParcelBody)
		uItemBodyLength  := consumeShort (&abyParcelBody)

		abyItemBody := abyParcelBody [ : uItemBodyLength] // constrained

		abyParcelBody = abyParcelBody [uItemBodyLength : ] // advance to the next MetadataItem

		if uItemContentType == /*RSSummary*/ 3 {
			err = rows.m_stmt.m_con.makeDriverError (nil, 0, "Summary items are not supported")
			return
		}

		switch uItemLayoutType { case /*FullContent*/ 1, /*LimitedContent*/ 2:

			if uPrevItemContentType == uItemContentType {
				nColumn++
			} else {
				nColumn = 1
				uPrevItemContentType = uItemContentType
			}
		} // end switch uItemLayoutType

		col := columnMetadata {}

		var bInParam bool

		switch uItemLayoutType {

			case /*LimitedContent*/ 2:

				col.M_uRawDataType         = consumeShort (&abyItemBody)
				col.M_uCookedDataType      = cookedDataType (col.M_uRawDataType)
				bInParam                   = isInParam (col.M_uRawDataType)
				col.M_uByteCount           = consumeLong (&abyItemBody)
				col.M_uPrecision           = uint8 (consumeShort (&abyItemBody))
				col.M_uIntervalDigits      = consumeShort (&abyItemBody)
				col.M_uScale               = uint8 (consumeShort (&abyItemBody))

			case /*FullContent*/ 1:

				col.M_sDatabaseName              = string (rows.m_stmt.m_con.consumeVarchar (&abyItemBody))
				col.M_sObjectName                = string (rows.m_stmt.m_con.consumeVarchar (&abyItemBody))
				col.M_sName                      = string (rows.m_stmt.m_con.consumeVarchar (&abyItemBody))
				col.M_uPosition                  = consumeShort (&abyItemBody)
				col.M_sAsClauseName              = string (rows.m_stmt.m_con.consumeVarchar (&abyItemBody))
				col.M_sTitle                     = string (rows.m_stmt.m_con.consumeVarchar (&abyItemBody))
				col.M_sFormat                    = string (rows.m_stmt.m_con.consumeVarchar (&abyItemBody))
				col.M_sDefaultValue              = string (rows.m_stmt.m_con.consumeVarchar (&abyItemBody))
				col.M_bIdentityColumn            = consumeByte (&abyItemBody) == 'Y'
				col.M_bDefinitelyWritable        = consumeByte (&abyItemBody) == 'Y'
				col.M_bNullable                  = consumeByte (&abyItemBody) == 'Y'
				col.M_bMayBeNull                 = consumeByte (&abyItemBody) == 'Y'
				col.M_bSearchable                = consumeByte (&abyItemBody) == 'Y'
				col.M_bWritable                  = consumeByte (&abyItemBody) == 'Y'
				col.M_uRawDataType               = consumeShort (&abyItemBody)
				col.M_uCookedDataType            = cookedDataType (col.M_uRawDataType)
				bInParam                         = isInParam (col.M_uRawDataType)
				col.M_uUDTKind                   = consumeShort (&abyItemBody)
				col.M_sUDTName                   = string (rows.m_stmt.m_con.consumeVarchar (&abyItemBody))
				col.M_sDataTypeMiscInfo          = string (rows.m_stmt.m_con.consumeVarchar (&abyItemBody))
				col.M_uByteCount                 = consumeLong (&abyItemBody)
				col.M_uPrecision                 = uint8 (consumeShort (&abyItemBody))
				col.M_uIntervalDigits            = consumeShort (&abyItemBody)
				col.M_uScale                     = uint8 (consumeShort (&abyItemBody))
				col.M_uServerCharsetCode         = consumeByte (&abyItemBody)
				col.M_uMaxCharacterCount         = consumeLong (&abyItemBody)
				col.M_bCaseSensitive             = consumeByte (&abyItemBody) == 'Y'
				col.M_bSigned                    = consumeByte (&abyItemBody) == 'Y'
				col.M_bKeyColumn                 = consumeByte (&abyItemBody) == 'Y'
				col.M_bUnique                    = consumeByte (&abyItemBody) == 'Y'
				col.M_bExpression                = consumeByte (&abyItemBody) == 'Y'
				col.M_bSortable                  = consumeByte (&abyItemBody) == 'Y'
				col.M_uSPParameterDirection      = consumeByte (&abyItemBody)

				// This portion of the FullContentMetadataItem is present in TD 13.10 or later
				if len (abyItemBody) >= 7 {

					col.M_uStructDepth    = consumeShort (&abyItemBody)
					col.M_uTemporal       = consumeByte (&abyItemBody)
					col.M_sAttributeName  = string (rows.m_stmt.m_con.consumeVarchar (&abyItemBody))
					col.M_uServerDataType = consumeShort (&abyItemBody)

				} // end if len abyItemBody

				// This portion of the FullContentMetadataItem is present in TD 14.0 or later
				if len (abyItemBody) >= 2 {

					col.M_nArrayNumberOfDimensions = int (consumeShort (&abyItemBody))

					col.M_auArrayMaxCardinalities = make ([] uint32, col.M_nArrayNumberOfDimensions)
					for i := 0 ; i < col.M_nArrayNumberOfDimensions ; i++ {
						col.M_auArrayMaxCardinalities [i] = consumeInt (&abyItemBody)
					}

					col.M_auArrayLowerBounds = make ([] uint32, col.M_nArrayNumberOfDimensions)
					for i := 0 ; i < col.M_nArrayNumberOfDimensions ; i++ {
						col.M_auArrayLowerBounds [i] = consumeInt (&abyItemBody)
					}
				} // end if len abyItemBody

				// This portion of the FullContentMetadataItem is present when VSD support is available and IncludeCatalogName option was set to 'Y'
				if rows.m_stmt.m_con.m_config.M_uVSDSupport >= 1 && len (abyItemBody) >= 2 {

					nPeekByteCount := int (binary.BigEndian.Uint16 (abyItemBody))
					nNeededLength  := 2 + nPeekByteCount
					if len (abyItemBody) >= nNeededLength {

						col.M_sCatalogName = string (rows.m_stmt.m_con.consumeVarchar (&abyItemBody))

						// If catalog name is "TD_LOCAL" and local_catalog connection parameter is false, then provide an empty string for catalog name
						if strings.EqualFold (col.M_sCatalogName, "TD_LOCAL") && ! rows.m_stmt.m_con.m_params.M_bLocalCatalog {
							col.M_sCatalogName = ""
						}
					}
				} // end if VSD support
		} // end switch uItemLayoutType

		if col.M_uCookedDataType == T_NUMBER {
			if col.M_uPrecision == 0x80 { col.M_uPrecision = 40 }
			if col.M_uScale     == 0x80 { col.M_uScale     =  0 }
		}

		if rows.m_stmt.m_con.hasDebugLog () {

			var sFields string
			switch uItemLayoutType { case /*FullContent*/ 1, /*LimitedContent*/ 2:

				sFields = fmt.Sprintf (" nColumn=%v %+v", nColumn, col)
			}

			debugLog (fmt.Sprintf ("parseStatementInfoParcel: uItemLayoutType=%v-%v uItemContentType=%v-%v%v", uItemLayoutType, layoutType (uItemLayoutType), uItemContentType, contentType (uItemContentType), sFields))

		} // end if hasDebugLog

		switch uItemContentType { case /*ParameterMarker*/ 1, /*RSColumn*/ 2, /*AGKColumn*/ 4, /*SPParameter*/ 5, /*SPRSColumn*/ 6:

			switch uItemLayoutType { case /*FullContent*/ 1, /*LimitedContent*/ 2:

				uCookedDataType := func () uint16 { if col.M_uServerDataType != 0 { return col.M_uServerDataType } ; return col.M_uCookedDataType } ()
				_, col.m_bHasVarLength, col.m_bHasPrecScale, _, col.M_sTypeName = dataTypeInfo (uCookedDataType)

				sColumnName := func () string { switch {
					case rows.m_bColumnName        : return col.M_sName
					case col.M_sAsClauseName != "" : return col.M_sAsClauseName
					case col.M_sName         != "" : return col.M_sName
					default                        : return col.M_sTitle
					} } ()

				switch uItemContentType {

					case /*ParameterMarker*/ 1:
						rows.m_aParamMetadata  = append (rows.m_aParamMetadata , col)

					default:
						if ! bInParam {
							rows.m_aColumnMetadata   = append (rows.m_aColumnMetadata   , col)
							rows.m_asColumnNames     = append (rows.m_asColumnNames     , sColumnName)
							rows.m_atColumnScanTypes = append (rows.m_atColumnScanTypes , outboundType (rows.m_bTeradataValues, col.M_uCookedDataType))
						} // end if ! bInParam

				} // end switch uItemContentType
			} // end switch uItemLayoutType
		} // end switch uItemContentType

	} // end for len abyParcelBody

	return

} // end method parseStatementInfoParcel

func debugLogParcelHeaders (sTitle string, aabyParcelHeaders [][] byte) {

	for i, abyParcelHeader := range aabyParcelHeaders {

		uFlavor := cookedParcelFlavor (abyParcelHeader)
		debugLog (fmt.Sprintf ("%v: Parcel %v of %v %v %v", sTitle, (i + 1), len (aabyParcelHeaders), parcelFlavor (uFlavor), uFlavor))
	}
} // end debugLogParcelHeaders

func (rows *TeradataRows) IsClosed () bool {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter IsClosed %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave IsClosed %v", rows)) } ()
	}

	return rows.m_bClosed

} // end method IsClosed

func (rows *TeradataRows) Close () (err error) { // satisfies driver.Rows

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Rows.Close %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave Rows.Close %v err=%v", rows, err)) } ()
	}

	defer func () { // catch and wrap any panic
		if ex := recover () ; ex != nil {
			err = rows.m_stmt.m_con.makeDriverError (err, 0, formatRecover ("Cannot close database response", ex)) // preserve existing err (if any) as cause
		}
	} ()

	fxpManager := rows.m_fastexportManager
	if fxpManager == nil && rows.m_stmt.m_con.m_params.M_uConnectFunction == 1 && rows.m_byLOBSelect == 'S' {
		if rows.m_stmt.m_con.hasDebugLog () {
			debugLog ("Avoiding close because this FastExport is still in use")
		}
		return
	}

	if rows.m_bClosed { return }
	rows.m_bClosed = true

	bPartitionDBCCONS := strings.EqualFold (rows.m_stmt.m_con.m_params.M_sPartition, "DBCCONS")
	if bPartitionDBCCONS { return }

	var fxpErrors error
	defer func () {
		err = chainErrors (fxpErrors, err)
	} ()

	if fxpManager != nil {
		if rows.m_stmt.m_con.hasDebugLog () {
			debugLog (fmt.Sprintf ("found rows.m_fastexportManager=%v", fxpManager))
		}

		ctx := context.Background ()
		if rows.m_uRequestTimeout > 0 {
			var cancelFunc context.CancelFunc
			ctx, cancelFunc = context.WithTimeout (ctx, time.Duration (rows.m_uRequestTimeout) * time.Second)
			defer cancelFunc () // release the derived context if we finish before the timeout expires, leaving the parent context intact
		}

		fxpErrors = chainErrors (fxpErrors, fxpManager.closeFastExport (ctx))

		if rows.m_stmt.m_con.hasDebugLog () {
			debugLog (fmt.Sprintf ("delete rows.m_fastexportManager=%v", fxpManager))
		}

		rows.m_fastexportManager = nil

		if rows.m_stmt.m_con.hasDebugLog () {
			debugLog (fmt.Sprintf ("delete from rows.m_stmt.m_con.m_mapRequestToManager key=%v", rows.m_sNativeSQL))
		}

		delete (fxpManager.getAppRows ().m_stmt.m_con.m_mapRequestToManager, strings.TrimSpace (rows.m_sNativeSQL))
		fxpManager.getAppRows ().m_bFreedSpool = true
		return fxpManager.getAppRows ().Close ()
	}

	// We ignore the possible race condition in which a different thread deletes the map entry,
	// because the built-in delete function is a no-op when the map does not contain the key.

	if rows.m_uRowsHandle != 0 {
		g_mutexRowsHandles.Lock () // write lock
		delete (g_mapRowsHandles, rows.m_uRowsHandle)
		g_mutexRowsHandles.Unlock ()
	}

	if rows.m_bFreedSpool { return }
	rows.m_bFreedSpool = true

	abyContinueRequestMsg, errToken := rows.initControlData (MESSAGE_KIND_CONTINUE, rows.m_uRequestNumber, makeParcel (PCLCANCEL))

	if rows.m_stmt.m_con.hasDebugLog () && errToken != nil {
		debugLog (fmt.Sprintf ("Rows.Close ignoring OIDC/OAuth token error: %v", errToken))
	}

	abyContinueResponseMsg, _, err := rows.m_stmt.m_con.sendAndReceive (&NetworkIO { abyMsgToSend: abyContinueRequestMsg, rows: rows })
	if err != nil { return }

	if rows.m_stmt.m_con.hasDebugLog () {
		aabyParcelHeaders := getParcelHeadersFromMessage (abyContinueResponseMsg)
		debugLogParcelHeaders ("Rows.Close", aabyParcelHeaders)
	}

	err = rows.m_stmt.m_con.processMessageSuccessOrError (abyContinueResponseMsg, /*bRequired*/ false)
	if err != nil { return }

	return

} // end method Close

func (rows *TeradataRows) fetchNextParcel () (err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter fetchNextParcel %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave fetchNextParcel %v", rows)) } ()
	}

	if hasNextParcel (rows.m_abyCurrentParcelHeader) {

		rows.m_abyCurrentParcelHeader = nextParcel (rows.m_abyCurrentParcelHeader)
		return
	}

	abyContinueRequestMsg, errToken := rows.initControlData (MESSAGE_KIND_CONTINUE, rows.m_uRequestNumber,
		makeParcel (func () uint16 { if rows.keepResponse () { return PCLBIGKEEPRESP } ; return PCLBIGRESP } (),
			/*Field MaxResponseMessageBodySize*/ fourBytes (rows.m_uResponseLimit)))

	if rows.m_stmt.m_con.hasDebugLog () && errToken != nil {
		debugLog (fmt.Sprintf ("fetchNextParcel ignoring OIDC/OAuth token error: %v", errToken))
	}

	abyContinueResponseMsg, _, err := rows.m_stmt.m_con.sendAndReceive (&NetworkIO { abyMsgToSend: abyContinueRequestMsg, rows: rows })
	if err != nil { return }

	err = rows.m_stmt.m_con.processMessageSuccessOrError (abyContinueResponseMsg, /*bRequired*/ false)
	if err != nil { return }

	aabyParcelHeaders := getParcelHeadersFromMessage (abyContinueResponseMsg)

	if rows.m_stmt.m_con.hasDebugLog () { debugLogParcelHeaders ("fetchNextParcel", aabyParcelHeaders) }

	rows.processEndRequest (aabyParcelHeaders)

	rows.m_abyCurrentParcelHeader = aabyParcelHeaders [0]
	return

} // end method fetchNextParcel

func (rows *TeradataRows) safeMarshal (o any) (sOutput string) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter safeMarshal %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave safeMarshal %v", rows)) } ()
	}

	aby, err := json.Marshal (o)
	if err != nil {

		if rows.m_stmt.m_con.hasDebugLog () { debugLog (fmt.Sprintf ("safeMarshal: %v", err)) }
		return
	}

	sOutput = string (aby)
	return

} // end method safeMarshal

func (rows *TeradataRows) outbound (val TeradataValue) any {

	if rows.m_bTeradataValues && rows.m_sExportPathName == "" { return val }

	return val.AsGoType ()

} // end method outbound

func (rows *TeradataRows) Next (dest [] driver.Value) (err error) { // satisfies driver.Rows

	// This method must deserialize the next row of result set column values into the provided slice.
	// The provided slice will be the same size as the prior return value from the Columns method.
	// This method must return io.EOF when there are no more rows.

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Rows.Next %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave Rows.Next %v err=%v", rows, err)) } ()
	}

	defer func () { // catch and wrap any panic
		if ex := recover () ; ex != nil {
			err = rows.m_stmt.m_con.makeDriverError (err, 0, formatRecover ("Cannot read row from the database", ex)) // preserve existing err (if any) as cause
		}
	} ()

	bPartitionDBCCONS := strings.EqualFold (rows.m_stmt.m_con.m_params.M_sPartition, "DBCCONS")

	switch rows.m_nFakeState {

		case returnRow:

			dest [0] = rows.m_sNativeSQL

			if ! rows.m_bNoExecute {

				// data types must match s_atFakeColumnScanTypes
				dest [1] = int32 (/*uint32*/ rows.m_uRequestNumber)
				dest [2] = int32 (/*uint32*/ rows.m_uStatementNumber)
				dest [3] = int16 (/*uint16*/ rows.m_uActivityType)
				dest [4] = int64 (/*uint64*/ rows.m_uActivityCount)
				dest [5] = int32 (/*uint16*/ rows.m_uWarningCode)
				dest [6] = rows.m_sWarningMsg
				dest [7] = rows.safeMarshal (rows.m_aColumnMetadata)
				dest [8] = rows.safeMarshal (rows.m_aParamMetadata)
			}

			rows.m_nFakeState = resultDone
			return

		case resultDone:

			err = io.EOF
			return

	} // end switch

	fxpManager := rows.m_fastexportManager
	if fxpManager != nil && rows.m_stmt.m_con.m_params.M_uConnectFunction == 1 {
		rows = fxpManager.getFastExportRows ()
		if rows == nil {
			rows = fxpManager.getNextFastExportRows ()
			if rows == nil { // if there are no rows found, then return
				err = io.EOF
				return
			}
		}
	}

	// No parcels available if SQL request text was an empty string and therefore not executed.
	if len (rows.m_abyCurrentParcelHeader) == 0 {
		err = io.EOF
		return
	}

	// No more parcels if an error occurred (that was converted to a warning).
	if errorIsReallyWarning (rows.m_uWarningCode) {
		err = io.EOF
		return
	}

	if cookedParcelFlavor (rows.m_abyCurrentParcelHeader) == PCLENDMULTIPARTRECORD {

		err = rows.fetchNextParcel ()
		if err != nil { return }
	}

	ctx := context.Background ()
	if rows.m_uRequestTimeout > 0 {
		var cancelFunc context.CancelFunc
		ctx, cancelFunc = context.WithTimeout (ctx, time.Duration (rows.m_uRequestTimeout) * time.Second)
		defer cancelFunc () // release the derived context if we finish before the timeout expires, leaving the parent context intact
	}

	if cookedParcelFlavor (rows.m_abyCurrentParcelHeader) == PCLENDREQUEST {

		if fxpManager != nil && fxpManager.getStatementNumber () == fxpManager.getControlRows ().m_uStatementNumber {

			index := fxpManager.getFastExportRowsIndex ()
			rows = fxpManager.getNextFastExportRows ()
			if rows == nil { // if there are no rows found, then return
				err = io.EOF
				return
			}

			if index < fxpManager.getFastExportRowsIndex () {
				return rows.Next (dest)
			}
		}

		if fxpManager != nil && fxpManager.getStatementNumber () != fxpManager.getControlRows ().m_uStatementNumber {

			fxpManager.initStatementBlock ()
			fxpManager.initBindMetadata ()

			err = fxpManager.requestResults (ctx)
			if err != nil { return }
		}

		if fxpManager != nil && fxpManager.getBlkNumberReceive () <= fxpManager.getBlkCount () {

			err = fxpManager.receiveResults (ctx)
			if err != nil { return }

			err = fxpManager.requestResults (ctx)
			if err != nil { return }

			rows = fxpManager.getFastExportRows ()
			if rows == nil {
				rows = fxpManager.getNextFastExportRows ()
				if rows == nil { // if there are no rows found, then return
					err = io.EOF
					return
				}
			}

			return rows.Next (dest)

		} else {

			if rows.m_stmt.m_con.hasDebugLog () {
				debugLog ("Rows.Next: got PCLENDREQUEST after consumed messagesResultSet")
			}

			err = io.EOF
			return
		}

	}

	if cookedParcelFlavor (rows.m_abyCurrentParcelHeader) == PCLSTMTSTATUS {

		if rows.m_stmt.m_con.hasDebugLog () {
			debugLog ("Rows.Next: positioned at PCLSTMTSTATUS and setting io.EOF")
		}

		err = io.EOF
		return
	}

	if bPartitionDBCCONS && cookedParcelFlavor (rows.m_abyCurrentParcelHeader) == PCLENDSTATEMENT {

		if rows.m_stmt.m_con.hasDebugLog () {
			debugLog ("Rows.Next: DBCCONS fetching next bundle")
		}

		rows.m_sNativeSQL = " "
		err = rows.executeSQLRequest (ctx, nil, nil)
		if err != nil { return }

		if rows.m_stmt.m_con.hasDebugLog () {
			debugLog (fmt.Sprintf ("Rows.Next: DBCCONS fetched bundle and now at parcel %v", parcelFlavor (cookedParcelFlavor (rows.m_abyCurrentParcelHeader))))
		}

		if cookedParcelFlavor (rows.m_abyCurrentParcelHeader) == PCLPROMPT {

			if peekNextCookedParcelFlavor (rows.m_abyCurrentParcelHeader) == PCLENDSTATEMENT {

				if rows.m_stmt.m_con.hasDebugLog () {
					debugLog ("Rows.Next: DBCCONS advancing to PCLENDSTATEMENT")
				}

				err = rows.fetchNextParcel ()
				if err != nil { return }
			}

			if peekNextCookedParcelFlavor (rows.m_abyCurrentParcelHeader) == PCLENDREQUEST {

				if rows.m_stmt.m_con.hasDebugLog () {
					debugLog ("Rows.Next: DBCCONS advancing to PCLENDREQUEST")
				}

				err = rows.fetchNextParcel ()
				if err != nil { return }
			}

			if rows.m_stmt.m_con.hasDebugLog () {
				debugLog ("Rows.Next: DBCCONS setting m_bPromptMode and io.EOF")
			}

			rows.m_stmt.m_con.m_bPromptMode = true
			err = io.EOF
			return

		} // end if PCLPROMPT
	} // end if bPartitionDBCCONS and PCLENDSTATEMENT

	if cookedParcelFlavor (rows.m_abyCurrentParcelHeader) == PCLENDSTATEMENT {

		if rows.m_stmt.m_con.hasDebugLog () {
			debugLog ("Rows.Next: positioned at PCLENDSTATEMENT and setting io.EOF")
		}

		if peekNextCookedParcelFlavor (rows.m_abyCurrentParcelHeader) == PCLENDREQUEST {

			if rows.m_stmt.m_con.hasDebugLog () {
				debugLog ("Rows.Next: consuming final PCLENDSTATEMENT and advancing to PCLENDREQUEST")
			}

			err = rows.fetchNextParcel ()
			if err != nil { return }

		} else {

			if rows.m_stmt.m_con.hasDebugLog () {
				debugLog ("Rows.Next: remain positioned at PCLENDSTATEMENT because another statement bundle follows")
			}
		}

		err = io.EOF
		return

	} // end if PCLENDSTATEMENT

	if uFlavor := cookedParcelFlavor (rows.m_abyCurrentParcelHeader) ; uFlavor != PCLRECORD && uFlavor != PCLMULTIPARTRECORD {

		err = rows.m_stmt.m_con.makeDriverError (nil, 0,
			fmt.Sprintf ("The Teradata Database provided unexpected data parcel %v %v",
				parcelFlavor (uFlavor),
				uFlavor))

		return
	}

	abyParcelBody             := constrainedParcelBody (rows.m_abyCurrentParcelHeader)

	err = rows.fetchNextParcel ()
	if err != nil { return }

	for n := 1 ; cookedParcelFlavor (rows.m_abyCurrentParcelHeader) == PCLMULTIPARTRECORD ; n++ {

		// On the first pass, copy the parcel body out of the message so we can append to it without altering the message buffer.
		if n == 1 { abyParcelBody = copyByteSlice (abyParcelBody) }

		// Concatenate the series of PCLMULTIPARTRECORD parcel bodies together.
		abyParcelBody = append (abyParcelBody, constrainedParcelBody (rows.m_abyCurrentParcelHeader) ...)

		err = rows.fetchNextParcel ()
		if err != nil { return }

	} // end for PCLMULTIPARTRECORD

	bHasNullIndicators := ! bPartitionDBCCONS
	nColumnCount       := len (rows.m_aColumnMetadata)

	nNullIndicatorByteCount := func () int { if bHasNullIndicators {
		return (nColumnCount - 1) / 8 + 1 }
		return 0 } ()

	abyNullIndicators := func () [] byte { if bHasNullIndicators {
		return abyParcelBody [ : nNullIndicatorByteCount] }
		return [] byte { 0 } } ()

	abyColumnValues := abyParcelBody [nNullIndicatorByteCount : ]

	for iColumn := 0 ; iColumn < nColumnCount ; iColumn++ {

		uCookedDataType       := rows.m_aColumnMetadata [iColumn].M_uCookedDataType
		uColumnByteCount      := rows.m_aColumnMetadata [iColumn].M_uByteCount
		uColumnScale          := rows.m_aColumnMetadata [iColumn].M_uScale

		bPresent              := abyNullIndicators [iColumn / 8] & (0x80 >> uint (iColumn % 8)) == 0

		if rows.m_stmt.m_con.hasDebugLog () {
			debugLog (fmt.Sprintf ("Rows.Next: iColumn=%v uCookedDataType=%v uColumnByteCount=%v uColumnScale=%v bPresent=%v len(abyColumnValues)=%v", iColumn, uCookedDataType, uColumnByteCount, uColumnScale, bPresent, len (abyColumnValues)))
		}

		if ! bPresent {

			// The scale field is used for null and non-null DECIMAL, null Interval...Second types, and for null and non-null Period(Time...) types.

			var uNullScale uint8
			switch uCookedDataType { case
				T_DECIMAL                         ,
				T_INTERVAL_DAY_TO_SECOND          ,
				T_INTERVAL_HOUR_TO_SECOND         ,
				T_INTERVAL_MINUTE_TO_SECOND       ,
				T_INTERVAL_SECOND                 ,
				T_PERIOD_TIME                     ,
				T_PERIOD_TIME_WITH_TIME_ZONE      ,
				T_PERIOD_TIMESTAMP                ,
				T_PERIOD_TIMESTAMP_WITH_TIME_ZONE : uNullScale = uColumnScale
			}

			// A non-zero byte count is only required for fixed-width data types.

			nPrefixByteCount, _, _, _, _ := dataTypeInfo (uCookedDataType)
			bFixedWidth := nPrefixByteCount == 0

			var uNullByteCount uint64
			if bFixedWidth { uNullByteCount = uColumnByteCount }

			dest [iColumn] = rows.outbound (newTeradataNull (uCookedDataType, uNullScale, uNullByteCount))

		} // end if ! bPresent

		switch uCookedDataType {

			case // multi-parcel types
				T_BLOB_AS_INLINE,
				T_CLOB_AS_INLINE,
				T_JSON,
				T_XML:

				if bPresent {

					uByteCount := consumeLong (&abyColumnValues)

					var abyValue [] byte
					if uCookedDataType == T_BLOB_AS_INLINE {
						abyValue = abyColumnValues [ : uByteCount]
					} else {
						abyValue = rows.m_stmt.m_con.deserializeChar (uByteCount, abyColumnValues)
					}

					if rows.m_nFileExporterType == CSV_FILE_TYPE && uCookedDataType == T_BLOB_AS_INLINE {

						err = rows.m_stmt.m_con.makeDriverError (nil, 546,
							fmt.Sprintf ("Cannot export result set to file %v because a data type of %v (%v) in column %v of the selected table %v.%v is not supported for CSV exports.",
								rows.m_sExportPathName,
								rows.m_aColumnMetadata [iColumn].M_sTypeName,
								uCookedDataType,
								iColumn + 1,
								rows.m_aColumnMetadata [iColumn].M_sDatabaseName,
								rows.m_aColumnMetadata [iColumn].M_sObjectName))

						return
					}

					dest [iColumn] = rows.outbound (newTeradataValue (uCookedDataType, /*uScale*/ 0, abyValue))
					abyColumnValues = abyColumnValues [uByteCount : ]

				} else {
					skipLong (&abyColumnValues)
				}

			case // variable-length byte array types
				T_BLOB_AS_LOCATOR                 ,
				T_CLOB_AS_LOCATOR                 ,
				T_VARBYTE                         ,
				T_LONG_VARBYTE                    ,
				T_PERIOD_TIMESTAMP                ,
				T_PERIOD_TIMESTAMP_WITH_TIME_ZONE ,
				T_XML_AS_LOCATOR                  ,
				T_JSON_AS_LOCATOR                 :

				if bPresent {

					var uValueScale uint8
					switch uCookedDataType { case
						T_PERIOD_TIMESTAMP                ,
						T_PERIOD_TIMESTAMP_WITH_TIME_ZONE : uValueScale = uColumnScale
					}

					uByteCount := binary.BigEndian.Uint16 (abyColumnValues)
					dest [iColumn] = rows.outbound (newTeradataValue (uCookedDataType, uValueScale, abyColumnValues [2 : 2 + uByteCount]))

					if rows.m_fileExporter != nil {

						switch uCookedDataType {
							case T_CLOB_AS_LOCATOR,
								 T_XML_AS_LOCATOR,
								 T_JSON_AS_LOCATOR:

									dest [iColumn], err = rows.fetchClobForExport (ctx, dest [iColumn].([] byte), uCookedDataType, iColumn)

							case T_VARBYTE,
								 T_LONG_VARBYTE,
								 T_BLOB_AS_LOCATOR:

								if rows.m_nFileExporterType == CSV_FILE_TYPE {

									err = rows.m_stmt.m_con.makeDriverError (nil, 546,
										fmt.Sprintf ("Cannot export result set to file %v because a data type of %v (%v) in column %v of the selected table %v.%v is not supported for CSV exports.",
											rows.m_sExportPathName,
											rows.m_aColumnMetadata [iColumn].M_sTypeName,
											uCookedDataType,
											iColumn + 1,
											rows.m_aColumnMetadata [iColumn].M_sDatabaseName,
											rows.m_aColumnMetadata [iColumn].M_sObjectName))

									return

								} // end if CSV_FILE_TYPE

						} // end switch

					} // end if m_fileExporter
				} // end if bPresent

				skipVarField (&abyColumnValues)

			case // fixed-width byte array types
				T_FLOAT                      ,
				T_INTEGER                    ,
				T_SMALLINT                   ,
				T_BIGINT                     ,
				T_BYTE                       ,
				T_DATE_INTEGER               ,
				T_BYTEINT                    ,
				T_PERIOD_DATE                ,
				T_PERIOD_TIME                ,
				T_PERIOD_TIME_WITH_TIME_ZONE :

				if bPresent {

					var uValueScale uint8
					switch uCookedDataType { case
						T_PERIOD_TIME                ,
						T_PERIOD_TIME_WITH_TIME_ZONE : uValueScale = uColumnScale
					}

					if rows.m_nFileExporterType == CSV_FILE_TYPE && uCookedDataType == T_BYTE {

						err = rows.m_stmt.m_con.makeDriverError (nil, 546,
							fmt.Sprintf ("Cannot export result set to file %v because a data type of %v (%v) in column %v of the selected table %v.%v is not supported for CSV exports.",
								rows.m_sExportPathName,
								rows.m_aColumnMetadata [iColumn].M_sTypeName,
								uCookedDataType,
								iColumn + 1,
								rows.m_aColumnMetadata [iColumn].M_sDatabaseName,
								rows.m_aColumnMetadata [iColumn].M_sObjectName))

						return
					}

					dest [iColumn] = rows.outbound (newTeradataValue (uCookedDataType, uValueScale, abyColumnValues [ : uColumnByteCount]))
				}

				abyColumnValues = abyColumnValues [uColumnByteCount : ]

			case // variable-length character types
				T_VARCHAR      ,
				T_LONG_VARCHAR :

				if bPresent {
					dest [iColumn] = rows.outbound (newTeradataValue (uCookedDataType, /*uScale*/ 0, rows.m_stmt.m_con.deserializeVarchar (abyColumnValues)))
				}

				skipVarField (&abyColumnValues)

			case // fixed-width character types
				T_CHAR                      ,
				T_DATE_ANSI                 ,
				T_TIME                      ,
				T_TIMESTAMP                 ,
				T_TIME_WITH_TIME_ZONE       ,
				T_TIMESTAMP_WITH_TIME_ZONE  ,
				T_INTERVAL_YEAR             ,
				T_INTERVAL_YEAR_TO_MONTH    ,
				T_INTERVAL_MONTH            ,
				T_INTERVAL_DAY              ,
				T_INTERVAL_DAY_TO_HOUR      ,
				T_INTERVAL_DAY_TO_MINUTE    ,
				T_INTERVAL_DAY_TO_SECOND    ,
				T_INTERVAL_HOUR             ,
				T_INTERVAL_HOUR_TO_MINUTE   ,
				T_INTERVAL_HOUR_TO_SECOND   ,
				T_INTERVAL_MINUTE           ,
				T_INTERVAL_MINUTE_TO_SECOND ,
				T_INTERVAL_SECOND           :

				if bPresent {
					dest [iColumn] = rows.outbound (newTeradataValue (uCookedDataType, /*uScale*/ 0, rows.m_stmt.m_con.deserializeChar (uColumnByteCount, abyColumnValues)))
				}

				abyColumnValues = abyColumnValues [uColumnByteCount : ]

			case T_DECIMAL:

				if bPresent {
					dest [iColumn] = rows.outbound (NewTeradataValueDecimal (abyColumnValues [ : uColumnByteCount], uColumnScale))
				}

				abyColumnValues = abyColumnValues [uColumnByteCount : ]

			case T_NUMBER:

				uByteCount := consumeByte (&abyColumnValues)

				// a zero bytecount is not followed by any other fields
				// a nonzero bytecount is followed by 2 byte scale and 1 to 17 bytes unscaled value
				if bPresent {
					dest [iColumn] = rows.outbound (newTeradataValue (uCookedDataType, /*uScale*/ 0, abyColumnValues [ : uByteCount]))
				}

				abyColumnValues = abyColumnValues [uByteCount : ]

			default:
				err = rows.m_stmt.m_con.makeDriverError (nil, 0, fmt.Sprintf ("Column %v unsupported data type %v", iColumn + 1, uCookedDataType))
				return

		} // end switch

	} // end for iColumn

	return

} // end method Next

func (rows *TeradataRows) fetchClobForExport (ctx context.Context, abyLocator [] byte, uCookedDataType uint16, iColumn int) (sClobValue string, err error) {

	tval := func () TeradataValue { switch uCookedDataType {

		case T_CLOB_AS_LOCATOR: return NewTeradataValueClobLocator (abyLocator)
		case T_JSON_AS_LOCATOR: return NewTeradataValueJsonLocator (abyLocator)
		case T_XML_AS_LOCATOR : return NewTeradataValueXmlLocator  (abyLocator)

		default: return TeradataValue { m_err : rows.m_stmt.m_con.makeDriverError (nil, 0,
			fmt.Sprintf ("Column %v unsupported clob locator data type %v",
				iColumn + 1,
				uCookedDataType)) }
	} }()

	if tval.Err () != nil { return }

	r, err := rows.m_stmt.m_con.QueryContext (ctx, "SELECT ?", [] driver.NamedValue { driver.NamedValue {Ordinal: 1, Value: tval}})
	if err != nil { return }

	dest := make ([] driver.Value, 1)
	err = r.Next (dest)
	if errors.Is (err, io.EOF) { err = nil}

	var sClob string
	if tLobval, bIsTeraValue := dest [0].(TeradataValue) ; bIsTeraValue {
		sClob = tLobval.String ()
	} else {
		sClob = dest [0].(string)
	}

	return sClob, err

} // end method fetchClobForExport

var s_asFakeColumnNames     = [] string       { "NativeSQL"  , "RequestNumber", "StatementNumber", "ActivityType", "ActivityCount", "WarningCode", "WarningMessage", "ColumnMetadata", "ParameterMetadata" }
var s_asFakeColumnTypeNames = [] string       { "VARCHAR"    , "INTEGER"      , "INTEGER"        , "SMALLINT"    , "BIGINT"       , "INTEGER"    , "VARCHAR"       , "VARCHAR"       , "VARCHAR"           }
var s_anFakeColumnLengths   = [] int64        { math.MaxInt32, 0              , 0                , 0             , 0              , 0            , math.MaxInt32   , math.MaxInt32   , math.MaxInt32       }
var s_anFakeColumnHasVarLen = [] bool         { true         , false          , false            , false         , false          , false        , true            , true            , true                }
var s_atFakeColumnScanTypes = [] reflect.Type { T_string     , T_int32        , T_int32          , T_int16       , T_int64        , T_int32      , T_string        , T_string        , T_string            }

func (rows *TeradataRows) Columns () [] string { // satisfies driver.Rows

	// This method must return the names of the result set columns.
	// This method should return empty string for each unknown result set column name.
	// The sql package assumes the result set column count equals the length of this method's return value.

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Rows.Columns %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave Rows.Columns %v", rows)) } ()
	}

	if rows.m_fastexportManager != nil && rows.m_stmt.m_con.m_params.M_uConnectFunction == 1 {
		rows.m_fastexportManager.copyColMDToControlRows ()
	}

	if rows.m_bNoExecute { return s_asFakeColumnNames [ : 1]}

	if rows.m_nFakeState != turnedOff { return s_asFakeColumnNames }

	return rows.m_asColumnNames

} // end method Columns

func (rows *TeradataRows) ColumnTypeDatabaseTypeName (iColumn int) string { // satisfies driver.RowsColumnTypeDatabaseTypeName

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter RowsColumnTypeDatabaseTypeName.ColumnTypeDatabaseTypeName col=%v %v", iColumn, rows))
		defer func () { traceLog (fmt.Sprintf ("< leave RowsColumnTypeDatabaseTypeName.ColumnTypeDatabaseTypeName col=%v %v", iColumn, rows)) } ()
	}

	if rows.m_nFakeState != turnedOff { return s_asFakeColumnTypeNames [iColumn] }

	return rows.m_aColumnMetadata [iColumn].M_sTypeName

} // end method ColumnTypeDatabaseTypeName

func (rows *TeradataRows) ColumnTypeLength (iColumn int) (int64, bool) { // satisfies driver.RowsColumnTypeLength

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter RowsColumnTypeLength.ColumnTypeLength col=%v %v", iColumn, rows))
		defer func () { traceLog (fmt.Sprintf ("< leave RowsColumnTypeLength.ColumnTypeLength col=%v %v", iColumn, rows)) } ()
	}

	if rows.m_nFakeState != turnedOff { return s_anFakeColumnLengths [iColumn], s_anFakeColumnHasVarLen [iColumn] }

	return int64 (rows.m_aColumnMetadata [iColumn].M_uByteCount), rows.m_aColumnMetadata [iColumn].m_bHasVarLength

} // end method ColumnTypeLength

func (rows *TeradataRows) ColumnTypeNullable (iColumn int) (bool, bool) { // satisfies driver.RowsColumnTypeNullable

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter RowsColumnTypeNullable.ColumnTypeNullable col=%v %v", iColumn, rows))
		defer func () { traceLog (fmt.Sprintf ("< leave RowsColumnTypeNullable.ColumnTypeNullable col=%v %v", iColumn, rows)) } ()
	}

	if rows.m_nFakeState != turnedOff { return false, true } // non-nullable, known

	return rows.m_aColumnMetadata [iColumn].M_bNullable, true // known

} // end method ColumnTypeNullable

func (rows *TeradataRows) ColumnTypePrecisionScale (iColumn int) (int64, int64, bool) { // satisfies driver.RowsColumnTypePrecisionScale

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter RowsColumnTypePrecisionScale.ColumnTypePrecisionScale col=%v %v", iColumn, rows))
		defer func () { traceLog (fmt.Sprintf ("< leave RowsColumnTypePrecisionScale.ColumnTypePrecisionScale col=%v %v", iColumn, rows)) } ()
	}

	if rows.m_nFakeState != turnedOff { return 0, 0, false }

	return int64 (rows.m_aColumnMetadata [iColumn].M_uPrecision), int64 (rows.m_aColumnMetadata [iColumn].M_uScale), rows.m_aColumnMetadata [iColumn].m_bHasPrecScale

} // end method ColumnTypePrecisionScale

func (rows *TeradataRows) ColumnTypeScanType (iColumn int) reflect.Type { // satisfies driver.RowsColumnTypeScanType

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter RowsColumnTypeScanType.ColumnTypeScanType col=%v %v", iColumn, rows))
		defer func () { traceLog (fmt.Sprintf ("< leave RowsColumnTypeScanType.ColumnTypeScanType col=%v %v", iColumn, rows)) } ()
	}

	if rows.m_nFakeState != turnedOff { return s_atFakeColumnScanTypes [iColumn] }

	return rows.m_atColumnScanTypes [iColumn]

} // end method ColumnTypeScanType

func (rows *TeradataRows) HasNextResultSet () bool { // satisfies driver.RowsNextResultSet

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter RowsNextResultSet.HasNextResultSet %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave RowsNextResultSet.HasNextResultSet %v", rows)) } ()
	}

	if rows.m_bNoExecute {
		if rows.m_stmt.m_con.hasDebugLog () { debugLog ("HasNextResultSet return false because m_bNoExecute is true") }
		return false
	}

	if rows.m_nFakeState != turnedOff {
		if rows.m_stmt.m_con.hasDebugLog () { debugLog ("HasNextResultSet return true because m_nFakeState is not turnedOff") }
		return true
	}

	if len (rows.m_abyCurrentParcelHeader) == 0 {
		if rows.m_stmt.m_con.hasDebugLog () { debugLog ("HasNextResultSet return false: No parcels available because SQL request text was an empty string and therefore not executed") }
		return false
	}

	if errorIsReallyWarning (rows.m_uWarningCode) {
		if rows.m_stmt.m_con.hasDebugLog () { debugLog ("HasNextResultSet return false: No more parcels because an error occurred that was converted to a warning") }
		return false
	}

	bReturn := cookedParcelFlavor (rows.m_abyCurrentParcelHeader) != PCLENDREQUEST
	if rows.m_stmt.m_con.hasDebugLog () { debugLog (fmt.Sprintf ("HasNextResultSet return %v for parcel %v", bReturn, parcelFlavor (cookedParcelFlavor (rows.m_abyCurrentParcelHeader)))) }
	return bReturn

} // end method HasNextResultSet

func (rows *TeradataRows) NextResultSet () (err error) { // satisfies driver.RowsNextResultSet

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter RowsNextResultSet.NextResultSet %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave RowsNextResultSet.NextResultSet %v err=%v", rows, err)) } ()
	}

	defer func () { // catch and wrap any panic
		if ex := recover () ; ex != nil {
			err = rows.m_stmt.m_con.makeDriverError (err, 0, formatRecover ("Cannot advance to next result from the database", ex)) // preserve existing err (if any) as cause
		}
	} ()

	if rows.m_nFakeState != turnedOff {
		rows.m_nFakeState = turnedOff
		return
	}

	ctx := context.Background ()
	if rows.m_uRequestTimeout > 0 {
		var cancelFunc context.CancelFunc
		ctx, cancelFunc = context.WithTimeout (ctx, time.Duration (rows.m_uRequestTimeout) * time.Second)
		defer cancelFunc () // release the derived context if we finish before the timeout expires, leaving the parent context intact
	}

	if rows.m_fastexportManager != nil && rows.m_stmt.m_con.m_params.M_uConnectFunction == 1 {
		err = rows.m_fastexportManager.nextExportResultSet (ctx)
		if err != nil { return }
	}

	err = rows.consumeCurrentResultSet ()
	if err != nil { return }

	if ! rows.HasNextResultSet () {
		err = io.EOF
		return
	}

	aabyParcelHeaders := getParcelHeadersFromCurrent (rows.m_abyCurrentParcelHeader)

	err = rows.processResponseBundle (nil, aabyParcelHeaders) // may call fetchNextParcel
	if err != nil { return }

	return

} // end method NextResultSet

func (rows *TeradataRows) consumeCurrentResultSet () (err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter consumeCurrentResultSet %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave consumeCurrentResultSet %v", rows)) } ()
	}

	for {
		switch cookedParcelFlavor (rows.m_abyCurrentParcelHeader) {

			case PCLRECORD, PCLMULTIPARTRECORD, PCLENDMULTIPARTRECORD:

				err = rows.fetchNextParcel ()
				if err != nil { return }

			case PCLENDSTATEMENT:

				err = rows.fetchNextParcel ()
				return

			default:
				return
		}
	}
} // end method consumeCurrentResultSet

var reWhiteSpace = regexp.MustCompile ("\\s+")

func (rows *TeradataRows) isCreateProcedure () (bReturn bool) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter isCreateProcedure %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave isCreateProcedure bReturn=%v %v", bReturn, rows)) } ()
	}

	asTokens := reWhiteSpace.Split (rows.m_sNativeSQL, 3)

	bReturn = len (asTokens) > 2 &&
		(strings.EqualFold (asTokens [0], "CREATE") || strings.EqualFold (asTokens [0], "REPLACE")) &&
		strings.EqualFold (asTokens [1], "PROCEDURE")

	return

} // end method isCreateProcedure

const MAX_APH_SEG_SIZE int = 999500

func (rows *TeradataRows) executeCreateProcedure (ctx context.Context) (err error) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter executeCreateProcedure %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave executeCreateProcedure %v", rows)) } ()
	}

	bLastPass := false
	nQueryStartIndex := 0

	bForceAPH := altHeaderRequired ([] byte (rows.m_sNativeSQL))

	for uTSRSequenceNumber := 1 ; ! bLastPass ; uTSRSequenceNumber++  {

		sTSRBuffer := ""

		if len (rows.m_sNativeSQL) - nQueryStartIndex > MAX_APH_SEG_SIZE {

			nQueryEndIndex := nQueryStartIndex + MAX_APH_SEG_SIZE - utf8.UTFMax

			// Make sure we send multi-byte characters in the same buffer
			for !utf8.RuneStart (rows.m_sNativeSQL [nQueryEndIndex]) {
				nQueryEndIndex++
			}

			sTSRBuffer = rows.m_sNativeSQL [nQueryStartIndex : nQueryEndIndex]
			nQueryStartIndex += len (sTSRBuffer)

		} else {
			bLastPass = true
			sTSRBuffer = rows.m_sNativeSQL [nQueryStartIndex : len (rows.m_sNativeSQL)]
		}

		if wasInterrupted (ctx) {

			err = rows.m_stmt.m_con.makeInterruptError (ctx.Err (), false /*abort sent*/)

			// If the create procedure was interrupted (cancel/timeout) after sending the first sequence, the driver
			// must send zero-length data to the database, which causes the create to fail and abort the request.
			// We do not want the driver to send an abort in this situation, because that might put the database
			// into an invalid state such that it either hangs or returns error 8019 "The parcel stream is invalid".
			if uTSRSequenceNumber > 1 {

				abyStartRequestMsg, errToken := rows.composeTSRStartRequestMessage ("", uTSRSequenceNumber, true /*bLastPass*/, bForceAPH)

				if rows.m_stmt.m_con.hasDebugLog () && errToken != nil {
					debugLog (fmt.Sprintf ("executeCreateProcedure ignoring OIDC/OAuth token error: %v", errToken))
				}

				_, _, errSend := rows.m_stmt.m_con.sendAndReceive (&NetworkIO { abyMsgToSend: abyStartRequestMsg, rows: rows })
				err = chainErrors (err, errSend)

				rows.m_bFreedSpool = true
			}

			return

		} // if wasInterrupted

		abyStartRequestMsg, errToken := rows.composeTSRStartRequestMessage (sTSRBuffer, uTSRSequenceNumber, bLastPass, bForceAPH)

		if uTSRSequenceNumber == 1 && errToken != nil {
			err = errToken // first TSR message fails for OIDC/OAuth token refresh failure
			return
		}

		if rows.m_stmt.m_con.hasDebugLog () && errToken != nil {
			debugLog (fmt.Sprintf ("executeCreateProcedure TSR message %v ignoring OIDC/OAuth token error: %v", uTSRSequenceNumber, errToken))
		}

		var abyStartResponseMsg [] byte
		abyStartResponseMsg, _, err = rows.m_stmt.m_con.sendAndReceive (&NetworkIO { abyMsgToSend: abyStartRequestMsg, rows: rows })
		if err != nil { return }

		rows.m_uRequestNumber = binary.BigEndian.Uint32 (abyStartResponseMsg [32 : ])

		aabyParcelHeaders := getParcelHeadersFromMessage (abyStartResponseMsg)

		if rows.m_stmt.m_con.hasDebugLog () { debugLogParcelHeaders ("executeCreateProcedure", aabyParcelHeaders) }

		var errFile error
		if peekCookedParcelFlavor (0, aabyParcelHeaders) == PCLELICITFILE {

			aabyParcelHeaders, errFile, err = rows.processElicitFileParcel (ctx, aabyParcelHeaders [0])
			if err != nil { return }
		}

		if bLastPass { // processEndRequest

			for _, abyParcelHeader := range aabyParcelHeaders {

				if cookedParcelFlavor (abyParcelHeader) == PCLENDREQUEST {
					rows.m_bFreedSpool = true
					break
				}
			}
		}

		err = rows.processResponseBundle (errFile, aabyParcelHeaders) // may call fetchNextParcel
		if err != nil { return }

		err = rows.fetchMessagesFromResultSet ()
		if err != nil { return }

		if warningIsReallyError (rows.m_uWarningCode) {

			var errCause error // initialized to nil
			if rows.m_sWarningMsg != "" { errCause = errors.New (rows.m_sWarningMsg) } // contains stored procedure creation errors

			err = rows.m_stmt.m_con.formatDatabaseError (errCause, rows.m_uWarningCode, rows.m_sRawWarningMsg)

			if rows.m_stmt.m_con.hasDebugLog () { debugLog (fmt.Sprintf ("warningIsReallyError: %v %v", rows, err)) }

			return
		}
	}  // end for loop bLastPass

	return

} // end executeCreateProcedure

func (rows *TeradataRows) composeTSRStartRequestMessage (sTSRBuffer string, uTSRSequenceNumber int, bLastPass bool, bForceAPH bool) (abyStartRequestMsg [] byte, err error) {

	aabyRequestParcels := append ([][] byte (nil),
		makeParcelEx (bForceAPH, PCLMULTITSR,
			/*Field Sequence Number (short)*/ twoBytes (uint16 (uTSRSequenceNumber)),
			/*Field Last Message           */ [] byte { func () byte { if bLastPass { return 1 } ; return 0 } () },
		))

	if uTSRSequenceNumber == 1 {

		rows.m_byRPO = 'E'

		abyOptionsParcelBody := rows.composeOptionsParcelBody ()

		aabyRequestParcels = append (aabyRequestParcels,
			makeParcel (PCLOPTIONS, abyOptionsParcelBody),
		)
	} // end if

	aabyRequestParcels = append (aabyRequestParcels,
		makeParcel (PCLINDICREQ, [] byte (sTSRBuffer))) // makeParcel automatically composes alt-header if needed

	if uTSRSequenceNumber == 1 {
		aabyRequestParcels = append (aabyRequestParcels,
			makeParcel (PCLSPOPTIONS, [] byte {
				/*Field Print (unused)*/ 'N',
				/*Field SaveSPL       */ func () byte { if rows.m_stmt.m_con.m_params.M_bSaveSPL { return 'Y' } ; return 'N' } (),
			}))
	}

	aabyRequestParcels = append (aabyRequestParcels,
		makeParcel (PCLBIGRESP ,  // Must use PCLBIGRESP for TSR messages
			/*Field MaxResponseMessageBodySize*/ fourBytes (rows.m_uResponseLimit)))

	// initControlData only returns a non-nil error for an OIDC/OAuth token refresh failure, and the caller must choose the appropriate action.
	// Raise this error to the application for first TSR message, but ignore and debug log for other cases.
	abyStartRequestMsg, err = rows.initControlData (MESSAGE_KIND_START, NEW_REQUEST_NUMBER, aabyRequestParcels ...)
	return

} // end composeTSRStartRequestMessage

func TeradataRowsFromHandle (uLog uint64, uRowsHandle uint64) (rows *TeradataRows) {

	bTraceLog := (uLog & 1) != 0

	if bTraceLog {
		traceLog (fmt.Sprintf ("> enter TeradataRowsFromHandle uRowsHandle=%v", uRowsHandle))
		defer func () { traceLog (fmt.Sprintf ("< leave TeradataRowsFromHandle uRowsHandle=%v", uRowsHandle)) } ()
	}

	g_mutexRowsHandles.RLock () // read lock
	rows = g_mapRowsHandles [uRowsHandle]
	g_mutexRowsHandles.RUnlock ()
	return

} // end func TeradataRowsFromHandle

func (rows *TeradataRows) ActivityCount () (uint64, uint16, string) {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter ActivityCount %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave ActivityCount %v", rows)) } ()
	}

	return rows.m_uActivityCount, rows.m_uActivityType, activityType (rows.m_uActivityType)

} // end method ActivityCount

func (rows *TeradataRows) AvoidFreeingSpool () {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter AvoidFreeingSpool %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave AvoidFreeingSpool %v", rows)) } ()
	}

	rows.m_bFreedSpool = true

} // end method AvoidFreeingSpool

func (rows *TeradataRows) ResultPOSIXlt () bool {

	if rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter ResultPOSIXlt %v", rows))
		defer func () { traceLog (fmt.Sprintf ("< leave ResultPOSIXlt %v", rows)) } ()
	}

	return rows.m_bPOSIXlt

} // end method ResultPOSIXlt

func (rows *TeradataRows) createFileExporter (sExportPathName string, asColumnNames [] string, sFieldSeparator string, sFieldQuote string) (fileExp fileExporter, err error) {

	dest := make ([] driver.Value, len (asColumnNames))

	fileExp, err = newFileExporter (sExportPathName, CSV_FILE_TYPE, sFieldSeparator, sFieldQuote)
	if err != nil {
		err = rows.m_stmt.m_con.makeDriverError (err, 548, fmt.Sprintf ("Escape function TERADATA_WRITE_CSV specifies an inaccessible file %v", sExportPathName))
		return
	}

	for nCol, sColName := range asColumnNames {
		dest [nCol] = sColName
	}

	err = fileExp.exportRecord (dest, 0, false)
	return

}  // end method createFileExporter

func  (rows *TeradataRows) getExportFilePathName (sExportPathName string, nFileExporterResultSet int) string {

	if nFileExporterResultSet < 1 || sExportPathName == "" { return sExportPathName }

	nIndex := strings.LastIndex (sExportPathName, ".")
	if nIndex < 0 {
		return fmt.Sprintf ("%v_%v", sExportPathName, nFileExporterResultSet)
	}

	return fmt.Sprintf ("%v_%v%v", sExportPathName [ : nIndex], nFileExporterResultSet, sExportPathName [nIndex : ])

} // end method getExportFilePathName
