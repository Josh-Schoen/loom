// Copyright 2017 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"
)

var _ fmt.Stringer = &TeradataValue {} // implements fmt.Stringer

var BIG_ONE  = big.NewInt (1)
var BIG_TEN  = big.NewInt (10)

type TeradataValue struct {

	m_err               error     // indicates whether this is an invalid value due to parsing error, etc.
	m_bPresent          bool      // use m_abyValue when true (non-null), use m_uByteCount when false (null)
	m_uCookedDataType   uint16
	m_uScale            uint8     // use for null and non-null DECIMAL, for null Interval...Second types, and for null and non-null Period(Time...) types
	m_uByteCount        uint64    // non-zero byte count is only required for null values of fixed-width data types
	m_abyValue          [] byte   // for non-null character values, this contains UTF8-encoded characters, regardless of the session character set, making the UTF8 session character set the most efficient

} // end type TeradataValue

func newTeradataNull (uCookedDataType uint16, uScale uint8, uByteCount uint64) TeradataValue {

	return TeradataValue {

		m_uCookedDataType   : uCookedDataType,
		m_uScale            : uScale,
		m_uByteCount        : uByteCount,
	}
} // end func newTeradataNull

func newTeradataValue (uCookedDataType uint16, uScale uint8, abyValue [] byte) TeradataValue {

	return TeradataValue {

		m_bPresent          : true,
		m_uCookedDataType   : uCookedDataType,
		m_uScale            : uScale,
		m_abyValue          : abyValue,
	}
} // end func newTeradataValue

func NewTeradataNullVarchar                      (                              ) TeradataValue { return newTeradataBoundNull (T_VARCHAR                        , 0         , 0      , 0     , 0         ) }
func NewTeradataNullChar                         (uByteCount uint64             ) TeradataValue { return newTeradataBoundNull (T_CHAR                           , 0         , 0      , 0     , uByteCount) }
func NewTeradataNullLongVarchar                  (                              ) TeradataValue { return newTeradataBoundNull (T_LONG_VARCHAR                   , 0         , 0      , 0     , 0         ) }
func NewTeradataNullXml                          (                              ) TeradataValue { return newTeradataBoundNull (T_XML                            , 0         , 0      , 0     , 0         ) }
func NewTeradataNullJson                         (                              ) TeradataValue { return newTeradataBoundNull (T_JSON                           , 0         , 0      , 0     , 0         ) }
func NewTeradataNullFloat                        (                              ) TeradataValue { return newTeradataBoundNull (T_FLOAT                          , 0         , 0      , 0     , 0         ) }
func NewTeradataNullDecimal                      (uPrecision uint8, uScale uint8) TeradataValue { return newTeradataBoundNull (T_DECIMAL                        , uPrecision, 0      , uScale, 0         ) }
func NewTeradataNullInteger                      (                              ) TeradataValue { return newTeradataBoundNull (T_INTEGER                        , 0         , 0      , 0     , 0         ) }
func NewTeradataNullSmallint                     (                              ) TeradataValue { return newTeradataBoundNull (T_SMALLINT                       , 0         , 0      , 0     , 0         ) }
func NewTeradataNullBigint                       (                              ) TeradataValue { return newTeradataBoundNull (T_BIGINT                         , 0         , 0      , 0     , 0         ) }
func NewTeradataNullNumber                       (                              ) TeradataValue { return newTeradataBoundNull (T_NUMBER                         , 0         , 0      , 0     , 0         ) }
func NewTeradataNullVarbyte                      (                              ) TeradataValue { return newTeradataBoundNull (T_VARBYTE                        , 0         , 0      , 0     , 0         ) }
func NewTeradataNullByte                         (uByteCount uint64             ) TeradataValue { return newTeradataBoundNull (T_BYTE                           , 0         , 0      , 0     , uByteCount) }
func NewTeradataNullLongVarbyte                  (                              ) TeradataValue { return newTeradataBoundNull (T_LONG_VARBYTE                   , 0         , 0      , 0     , 0         ) }
func NewTeradataNullDateANSI                     (                              ) TeradataValue { return newTeradataBoundNull (T_DATE_ANSI                      , 0         , 0      , 0     , 0         ) }
func NewTeradataNullDateInteger                  (                              ) TeradataValue { return newTeradataBoundNull (T_DATE_INTEGER                   , 0         , 0      , 0     , 0         ) }
func NewTeradataNullByteint                      (                              ) TeradataValue { return newTeradataBoundNull (T_BYTEINT                        , 0         , 0      , 0     , 0         ) }
func NewTeradataNullTime                         (                  uScale uint8) TeradataValue { return newTeradataBoundNull (T_TIME                           , 0         , 0      , uScale, 0         ) }
func NewTeradataNullTimestamp                    (                  uScale uint8) TeradataValue { return newTeradataBoundNull (T_TIMESTAMP                      , 0         , 0      , uScale, 0         ) }
func NewTeradataNullTimeWithTimeZone             (                  uScale uint8) TeradataValue { return newTeradataBoundNull (T_TIME_WITH_TIME_ZONE            , 0         , 0      , uScale, 0         ) }
func NewTeradataNullTimestampWithTimeZone        (                  uScale uint8) TeradataValue { return newTeradataBoundNull (T_TIMESTAMP_WITH_TIME_ZONE       , 0         , 0      , uScale, 0         ) }
func NewTeradataNullIntervalYear                 (uDigits uint64                ) TeradataValue { return newTeradataBoundNull (T_INTERVAL_YEAR                  , 0         , uDigits, 0     , 0         ) }
func NewTeradataNullIntervalYearToMonth          (uDigits uint64                ) TeradataValue { return newTeradataBoundNull (T_INTERVAL_YEAR_TO_MONTH         , 0         , uDigits, 0     , 0         ) }
func NewTeradataNullIntervalMonth                (uDigits uint64                ) TeradataValue { return newTeradataBoundNull (T_INTERVAL_MONTH                 , 0         , uDigits, 0     , 0         ) }
func NewTeradataNullIntervalDay                  (uDigits uint64                ) TeradataValue { return newTeradataBoundNull (T_INTERVAL_DAY                   , 0         , uDigits, 0     , 0         ) }
func NewTeradataNullIntervalDayToHour            (uDigits uint64                ) TeradataValue { return newTeradataBoundNull (T_INTERVAL_DAY_TO_HOUR           , 0         , uDigits, 0     , 0         ) }
func NewTeradataNullIntervalDayToMinute          (uDigits uint64                ) TeradataValue { return newTeradataBoundNull (T_INTERVAL_DAY_TO_MINUTE         , 0         , uDigits, 0     , 0         ) }
func NewTeradataNullIntervalDayToSecond          (uDigits uint64  , uScale uint8) TeradataValue { return newTeradataBoundNull (T_INTERVAL_DAY_TO_SECOND         , 0         , uDigits, uScale, 0         ) }
func NewTeradataNullIntervalHour                 (uDigits uint64                ) TeradataValue { return newTeradataBoundNull (T_INTERVAL_HOUR                  , 0         , uDigits, 0     , 0         ) }
func NewTeradataNullIntervalHourToMinute         (uDigits uint64                ) TeradataValue { return newTeradataBoundNull (T_INTERVAL_HOUR_TO_MINUTE        , 0         , uDigits, 0     , 0         ) }
func NewTeradataNullIntervalHourToSecond         (uDigits uint64  , uScale uint8) TeradataValue { return newTeradataBoundNull (T_INTERVAL_HOUR_TO_SECOND        , 0         , uDigits, uScale, 0         ) }
func NewTeradataNullIntervalMinute               (uDigits uint64                ) TeradataValue { return newTeradataBoundNull (T_INTERVAL_MINUTE                , 0         , uDigits, 0     , 0         ) }
func NewTeradataNullIntervalMinuteToSecond       (uDigits uint64  , uScale uint8) TeradataValue { return newTeradataBoundNull (T_INTERVAL_MINUTE_TO_SECOND      , 0         , uDigits, uScale, 0         ) }
func NewTeradataNullIntervalSecond               (uDigits uint64  , uScale uint8) TeradataValue { return newTeradataBoundNull (T_INTERVAL_SECOND                , 0         , uDigits, uScale, 0         ) }
func NewTeradataNullPeriodDate                   (                              ) TeradataValue { return newTeradataBoundNull (T_PERIOD_DATE                    , 0         , 0      , 0     , 0         ) }
func NewTeradataNullPeriodTime                   (                  uScale uint8) TeradataValue { return newTeradataBoundNull (T_PERIOD_TIME                    , 0         , 0      , uScale, 0         ) }
func NewTeradataNullPeriodTimeWithTimeZone       (                  uScale uint8) TeradataValue { return newTeradataBoundNull (T_PERIOD_TIME_WITH_TIME_ZONE     , 0         , 0      , uScale, 0         ) }
func NewTeradataNullPeriodTimestamp              (                  uScale uint8) TeradataValue { return newTeradataBoundNull (T_PERIOD_TIMESTAMP               , 0         , 0      , uScale, 0         ) }
func NewTeradataNullPeriodTimestampWithTimeZone  (                  uScale uint8) TeradataValue { return newTeradataBoundNull (T_PERIOD_TIMESTAMP_WITH_TIME_ZONE, 0         , 0      , uScale, 0         ) }
func NewTeradataNullLobDeferred                  (uType  uint16                 ) TeradataValue { return newTeradataBoundNull (uType                            , 0         , 0      , 0     , 0         ) }

func NewTeradataValueBlobLocator                 (aby    [] byte                ) TeradataValue { return newTeradataValue (T_BLOB_AS_LOCATOR                , 0     , aby                                 ) }
func NewTeradataValueClobLocator                 (aby    [] byte                ) TeradataValue { return newTeradataValue (T_CLOB_AS_LOCATOR                , 0     , aby                                 ) }
func NewTeradataValueJsonLocator                 (aby    [] byte                ) TeradataValue { return newTeradataValue (T_JSON_AS_LOCATOR                , 0     , aby                                 ) }
func NewTeradataValueXmlLocator                  (aby    [] byte                ) TeradataValue { return newTeradataValue (T_XML_AS_LOCATOR                 , 0     , aby                                 ) }
func NewTeradataValueXml                         (s      string                 ) TeradataValue { return newTeradataValue (T_XML                            , 0     , [] byte (s)                         ) }
func NewTeradataValueJson                        (s      string                 ) TeradataValue { return newTeradataValue (T_JSON                           , 0     , [] byte (s)                         ) }
func NewTeradataValueVarchar                     (s      string                 ) TeradataValue { return newTeradataValue (T_VARCHAR                        , 0     , [] byte (s)                         ) }
func NewTeradataValueChar                        (s      string                 ) TeradataValue { return newTeradataValue (T_CHAR                           , 0     , [] byte (s)                         ) }
func NewTeradataValueLongVarchar                 (s      string                 ) TeradataValue { return newTeradataValue (T_LONG_VARCHAR                   , 0     , [] byte (s)                         ) }
func NewTeradataValueFloat                       (d      float64                ) TeradataValue { return newTeradataValue (T_FLOAT                          , 0     , eightBytes (math.Float64bits (d))   ) }
func NewTeradataValueDecimal                     (aby    [] byte  , uScale uint8) TeradataValue { return newTeradataValue (T_DECIMAL                        , uScale, aby                                 ) }
func NewTeradataValueInteger                     (n      int32                  ) TeradataValue { return newTeradataValue (T_INTEGER                        , 0     , fourBytes  (uint32 (n))             ) }
func NewTeradataValueSmallint                    (n      int16                  ) TeradataValue { return newTeradataValue (T_SMALLINT                       , 0     , twoBytes   (uint16 (n))             ) }
func NewTeradataValueBigint                      (n      int64                  ) TeradataValue { return newTeradataValue (T_BIGINT                         , 0     , eightBytes (uint64 (n))             ) }
func NewTeradataValueNumber                      (aby    [] byte                ) TeradataValue { return newTeradataValue (T_NUMBER                         , 0     , aby                                 ) }
func NewTeradataValueVarbyte                     (aby    [] byte                ) TeradataValue { return newTeradataValue (T_VARBYTE                        , 0     , aby                                 ) }
func NewTeradataValueByte                        (aby    [] byte                ) TeradataValue { return newTeradataValue (T_BYTE                           , 0     , aby                                 ) }
func NewTeradataValueLongVarbyte                 (aby    [] byte                ) TeradataValue { return newTeradataValue (T_LONG_VARBYTE                   , 0     , aby                                 ) }
func NewTeradataValueDateANSI                    (s      string                 ) TeradataValue { return newTeradataValue (T_DATE_ANSI                      , 0     , [] byte (s)                         ) }
func NewTeradataValueDateInteger                 (aby    [] byte                ) TeradataValue { return newTeradataValue (T_DATE_INTEGER                   , 0     , aby                                 ) }
func NewTeradataValueByteint                     (n      int8                   ) TeradataValue { return newTeradataValue (T_BYTEINT                        , 0     , [] byte { byte (n) }                ) }
func NewTeradataValueTime                        (s      string                 ) TeradataValue { return newTeradataValue (T_TIME                           , 0     , [] byte (s)                         ) }
func NewTeradataValueTimestamp                   (s      string                 ) TeradataValue { return newTeradataValue (T_TIMESTAMP                      , 0     , [] byte (s)                         ) }
func NewTeradataValueTimeWithTimeZone            (s      string                 ) TeradataValue { return newTeradataValue (T_TIME_WITH_TIME_ZONE            , 0     , [] byte (s)                         ) }
func NewTeradataValueTimestampWithTimeZone       (s      string                 ) TeradataValue { return newTeradataValue (T_TIMESTAMP_WITH_TIME_ZONE       , 0     , [] byte (s)                         ) }
func NewTeradataValueIntervalYear                (s      string                 ) TeradataValue { return newTeradataValue (T_INTERVAL_YEAR                  , 0     , [] byte (s)                         ) }
func NewTeradataValueIntervalYearToMonth         (s      string                 ) TeradataValue { return newTeradataValue (T_INTERVAL_YEAR_TO_MONTH         , 0     , [] byte (s)                         ) }
func NewTeradataValueIntervalMonth               (s      string                 ) TeradataValue { return newTeradataValue (T_INTERVAL_MONTH                 , 0     , [] byte (s)                         ) }
func NewTeradataValueIntervalDay                 (s      string                 ) TeradataValue { return newTeradataValue (T_INTERVAL_DAY                   , 0     , [] byte (s)                         ) }
func NewTeradataValueIntervalDayToHour           (s      string                 ) TeradataValue { return newTeradataValue (T_INTERVAL_DAY_TO_HOUR           , 0     , [] byte (s)                         ) }
func NewTeradataValueIntervalDayToMinute         (s      string                 ) TeradataValue { return newTeradataValue (T_INTERVAL_DAY_TO_MINUTE         , 0     , [] byte (s)                         ) }
func NewTeradataValueIntervalDayToSecond         (s      string                 ) TeradataValue { return newTeradataValue (T_INTERVAL_DAY_TO_SECOND         , 0     , [] byte (s)                         ) }
func NewTeradataValueIntervalHour                (s      string                 ) TeradataValue { return newTeradataValue (T_INTERVAL_HOUR                  , 0     , [] byte (s)                         ) }
func NewTeradataValueIntervalHourToMinute        (s      string                 ) TeradataValue { return newTeradataValue (T_INTERVAL_HOUR_TO_MINUTE        , 0     , [] byte (s)                         ) }
func NewTeradataValueIntervalHourToSecond        (s      string                 ) TeradataValue { return newTeradataValue (T_INTERVAL_HOUR_TO_SECOND        , 0     , [] byte (s)                         ) }
func NewTeradataValueIntervalMinute              (s      string                 ) TeradataValue { return newTeradataValue (T_INTERVAL_MINUTE                , 0     , [] byte (s)                         ) }
func NewTeradataValueIntervalMinuteToSecond      (s      string                 ) TeradataValue { return newTeradataValue (T_INTERVAL_MINUTE_TO_SECOND      , 0     , [] byte (s)                         ) }
func NewTeradataValueIntervalSecond              (s      string                 ) TeradataValue { return newTeradataValue (T_INTERVAL_SECOND                , 0     , [] byte (s)                         ) }
func NewTeradataValuePeriodDate                  (aby    [] byte                ) TeradataValue { return newTeradataValue (T_PERIOD_DATE                    , 0     , aby                                 ) }
func NewTeradataValuePeriodTime                  (aby    [] byte  , uScale uint8) TeradataValue { return newTeradataValue (T_PERIOD_TIME                    , uScale, aby                                 ) }
func NewTeradataValuePeriodTimeWithTimeZone      (aby    [] byte  , uScale uint8) TeradataValue { return newTeradataValue (T_PERIOD_TIME_WITH_TIME_ZONE     , uScale, aby                                 ) }
func NewTeradataValuePeriodTimestamp             (aby    [] byte  , uScale uint8) TeradataValue { return newTeradataValue (T_PERIOD_TIMESTAMP               , uScale, aby                                 ) }
func NewTeradataValuePeriodTimestampWithTimeZone (aby    [] byte  , uScale uint8) TeradataValue { return newTeradataValue (T_PERIOD_TIMESTAMP_WITH_TIME_ZONE, uScale, aby                                 ) }
func NewTeradataValueLobDeferred                 (uType  uint16   , n      int32) TeradataValue { return newTeradataValue (uType                            , 0     , fourBytes (uint32 (n))              ) }

func newTeradataBoundNull (uCookedDataType uint16, uPrecision uint8, uDigits uint64, uScale uint8, uByteCount uint64) TeradataValue {

	switch uCookedDataType {

		case T_FLOAT                      : uByteCount =                 8
		case T_DECIMAL                    : uByteCount = byteCountFromDecimalPrecision (uPrecision)
		case T_INTEGER                    : uByteCount =                 4
		case T_SMALLINT                   : uByteCount =                 2
		case T_BIGINT                     : uByteCount =                 8
		case T_DATE_ANSI                  : uByteCount =                10
		case T_DATE_INTEGER               : uByteCount =                 4
		case T_BLOB_AS_DEFERRED           : uByteCount =                 4
		case T_CLOB_AS_DEFERRED           : uByteCount =                 4
		case T_JSON_AS_DEFERRED           : uByteCount =                 4
		case T_XML_AS_DEFERRED            : uByteCount =                 4
		case T_BYTEINT                    : uByteCount =                 1
		case T_TIME                       : uByteCount = timeByteCount ( 8          , uScale)
		case T_TIMESTAMP                  : uByteCount = timeByteCount (19          , uScale)
		case T_TIME_WITH_TIME_ZONE        : uByteCount = timeByteCount (14          , uScale)
		case T_TIMESTAMP_WITH_TIME_ZONE   : uByteCount = timeByteCount (25          , uScale)
		case T_INTERVAL_YEAR              : uByteCount =                 1 + uDigits
		case T_INTERVAL_YEAR_TO_MONTH     : uByteCount =                 4 + uDigits
		case T_INTERVAL_MONTH             : uByteCount =                 1 + uDigits
		case T_INTERVAL_DAY               : uByteCount =                 1 + uDigits
		case T_INTERVAL_DAY_TO_HOUR       : uByteCount =                 4 + uDigits
		case T_INTERVAL_DAY_TO_MINUTE     : uByteCount =                 7 + uDigits
		case T_INTERVAL_DAY_TO_SECOND     : uByteCount = timeByteCount (10 + uDigits, uScale)
		case T_INTERVAL_HOUR              : uByteCount =                 1 + uDigits
		case T_INTERVAL_HOUR_TO_MINUTE    : uByteCount =                 4 + uDigits
		case T_INTERVAL_HOUR_TO_SECOND    : uByteCount = timeByteCount ( 7 + uDigits, uScale)
		case T_INTERVAL_MINUTE            : uByteCount =                 1 + uDigits
		case T_INTERVAL_MINUTE_TO_SECOND  : uByteCount = timeByteCount ( 4 + uDigits, uScale)
		case T_INTERVAL_SECOND            : uByteCount = timeByteCount ( 1 + uDigits, uScale)
		case T_PERIOD_DATE                : uByteCount =                 8
		case T_PERIOD_TIME                : uByteCount =                12
		case T_PERIOD_TIME_WITH_TIME_ZONE : uByteCount =                16

	} // end switch

	switch uCookedDataType {

		case T_TIME                     ,
			 T_TIMESTAMP                ,
			 T_TIME_WITH_TIME_ZONE      ,
			 T_TIMESTAMP_WITH_TIME_ZONE : uScale = 0 // not needed after uByteCount is calculated

	} // end switch

	return newTeradataNull (uCookedDataType, uScale, uByteCount)

} // end func newTeradataBoundNull

func ParseTeradataValueDecimal (s string) TeradataValue {

	uScale, aby, err := parseDecimal (s, /*bPowerOf2Length*/ true)
	if err != nil {
		return TeradataValue { m_err : err }
	}

	return NewTeradataValueDecimal (aby, uint8 (uScale))

} // end func ParseTeradataValueDecimal

func ParseTeradataValueDecimalMinPrecision (s string, uMinPrecision uint8) (val TeradataValue) {

	if uMinPrecision > 38 {
		val = TeradataValue { m_err : formatError (nil, "Minimum precision %v is too large for decimal value %v", uMinPrecision, s) }
		return
	}

	val = ParseTeradataValueDecimal (s)
	if val.m_err != nil {
		return
	}

	val.m_abyValue = adjustDecimalLength (val.m_abyValue, uMinPrecision, val.m_uScale)
	return

} // end func ParseTeradataValueDecimalMinPrecision

func ParseTeradataValueNumber (s string) TeradataValue {

	uScale, aby, err := parseDecimal (s, /*bPowerOf2Length*/ false)
	if err != nil {
		return TeradataValue { m_err : err }
	}

	if len (aby) == 1 && aby [0] == 0 { // special case for zero
		aby = [] byte {}
	} else {
		aby = append (twoBytes (uScale), aby ...)
	}

	return NewTeradataValueNumber (aby)

} // end func ParseTeradataValueNumber

func ParseTeradataValueFloat (s string) TeradataValue {

	f, err := strconv.ParseFloat (s, 64)
	if err != nil {
		return TeradataValue { m_err : err }
	}

	return NewTeradataValueFloat (f)

} // end ParseTeradataValueFloat

func ParseTeradataValueDateInteger (s string) TeradataValue {

	aby, err := parseIntegerDate (s)
	if err != nil {
		return TeradataValue { m_err : err }
	}

	return NewTeradataValueDateInteger (aby)

} // end func ParseTeradataValueDateInteger

func ParseTeradataValuePeriodDate (sInput string) TeradataValue {

	as := strings.Split (sInput, ",")
	if len (as) != 2 {
		return TeradataValue { m_err : formatError (nil, "Period value must contain one comma: %v", sInput) }
	}

	var abyOutput [] byte

	for _, s := range as {

		aby, err := parseIntegerDate (s)
		if err != nil {
			return TeradataValue { m_err : err }
		}

		abyOutput = append (abyOutput, aby ...)
	}

	return NewTeradataValuePeriodDate (abyOutput)

} // end func ParseTeradataValuePeriodDate

func ParseTeradataValuePeriodTime (sInput string) TeradataValue {

	as := strings.Split (sInput, ",")
	if len (as) != 2 {
		return TeradataValue { m_err : formatError (nil, "Period value must contain one comma: %v", sInput) }
	}

	var abyOutput [] byte

	for _, s := range as {

		aby, err := parseBinaryTime (s)
		if err != nil {
			return TeradataValue { m_err : err }
		}

		abyOutput = append (abyOutput, aby ...)
	}

	uScale := maxUint8 (
		timeScale (T_TIME, uint64 (len (as [0]))),
		timeScale (T_TIME, uint64 (len (as [1]))))

	return NewTeradataValuePeriodTime (abyOutput, uScale)

} // end func ParseTeradataValuePeriodTime

func ParseTeradataValuePeriodTimeWithTimeZone (sInput string) TeradataValue {

	as := strings.Split (sInput, ",")
	if len (as) != 2 {
		return TeradataValue { m_err : formatError (nil, "Period value must contain one comma: %v", sInput) }
	}

	var abyOutput [] byte

	for _, s := range as {

		aby, err := parseBinaryTimeWithTimeZone (s)
		if err != nil {
			return TeradataValue { m_err : err }
		}

		abyOutput = append (abyOutput, aby ...)
	}

	uScale := maxUint8 (
		timeScale (T_TIME_WITH_TIME_ZONE, uint64 (len (as [0]))),
		timeScale (T_TIME_WITH_TIME_ZONE, uint64 (len (as [1]))))

	return NewTeradataValuePeriodTimeWithTimeZone (abyOutput, uScale)

} // end func ParseTeradataValuePeriodTimeWithTimeZone

func ParseTeradataValuePeriodTimestamp (sInput string) TeradataValue {

	as := strings.Split (sInput, ",")
	if len (as) != 2 {
		return TeradataValue { m_err : formatError (nil, "Period value must contain one comma: %v", sInput) }
	}

	var abyOutput [] byte

	for _, s := range as {

		aby, err := parseBinaryTimestamp (s)
		if err != nil {
			return TeradataValue { m_err : err }
		}

		abyOutput = append (abyOutput, aby ...)
	}

	uScale := maxUint8 (
		timeScale (T_TIMESTAMP, uint64 (len (as [0]))),
		timeScale (T_TIMESTAMP, uint64 (len (as [1]))))

	return NewTeradataValuePeriodTimestamp (abyOutput, uScale)

} // end func ParseTeradataValuePeriodTimestamp

func ParseTeradataValuePeriodTimestampWithTimeZone (sInput string) TeradataValue {

	as := strings.Split (sInput, ",")
	if len (as) != 2 {
		return TeradataValue { m_err : formatError (nil, "Period value must contain one comma: %v", sInput) }
	}

	var abyOutput [] byte

	for _, s := range as {

		aby, err := parseBinaryTimestampWithTimeZone (s)
		if err != nil {
			return TeradataValue { m_err : err }
		}

		abyOutput = append (abyOutput, aby ...)
	}

	uScale := maxUint8 (
		timeScale (T_TIMESTAMP_WITH_TIME_ZONE, uint64 (len (as [0]))),
		timeScale (T_TIMESTAMP_WITH_TIME_ZONE, uint64 (len (as [1]))))

	return NewTeradataValuePeriodTimestampWithTimeZone (abyOutput, uScale)

} // end func ParseTeradataValuePeriodTimestampWithTimeZone

// This is a value receiver method in order to work with a value inside an interface.

func (val TeradataValue) Err () error {

	return val.m_err

} // end method Err

// This is a value receiver method in order to work with a value inside an interface.

func (val TeradataValue) Valid () bool {

	return val.m_bPresent

} // end method Valid

// This is a value receiver method in order to work with a value inside an interface.

func (val TeradataValue) CookedDataType () uint16 {

	return val.m_uCookedDataType

} // end method CookedDataType

// This is a value receiver method in order to work with a value inside an interface.

func (val TeradataValue) ByteCount () uint64 {

	if val.m_bPresent { return uint64 (len (val.m_abyValue)) }

	return val.m_uByteCount

} // end method ByteCount

func timeRanges (uCookedDataType uint16) (uMinUnscaledByteCount uint64, uMaxUnscaledByteCount uint64, bScaled bool, uMinScaledByteCount uint64, uMaxScaledByteCount uint64) {

	switch uCookedDataType {

		case T_TIME                      : uMinUnscaledByteCount = uint64 (len (           "HH:MM:SS"      )) ; bScaled = true
		case T_TIMESTAMP                 : uMinUnscaledByteCount = uint64 (len ("YYYY:NN:DD HH:MM:SS"      )) ; bScaled = true
		case T_TIME_WITH_TIME_ZONE       : uMinUnscaledByteCount = uint64 (len (           "HH:MM:SS+ZZ:ZZ")) ; bScaled = true
		case T_TIMESTAMP_WITH_TIME_ZONE  : uMinUnscaledByteCount = uint64 (len ("YYYY:NN:DD HH:MM:SS+ZZ:ZZ")) ; bScaled = true

		case T_INTERVAL_YEAR             : uMinUnscaledByteCount = uint64 (len ("-Y"         )) ; uMaxUnscaledByteCount = uint64 (len ("-Yyyy"         ))
		case T_INTERVAL_YEAR_TO_MONTH    : uMinUnscaledByteCount = uint64 (len ("-Y NN"      )) ; uMaxUnscaledByteCount = uint64 (len ("-Yyyy NN"      ))
		case T_INTERVAL_MONTH            : uMinUnscaledByteCount = uint64 (len ("-N"         )) ; uMaxUnscaledByteCount = uint64 (len ("-Nnnn"         ))
		case T_INTERVAL_DAY              : uMinUnscaledByteCount = uint64 (len ("-D"         )) ; uMaxUnscaledByteCount = uint64 (len ("-Dddd"         ))
		case T_INTERVAL_DAY_TO_HOUR      : uMinUnscaledByteCount = uint64 (len ("-D HH"      )) ; uMaxUnscaledByteCount = uint64 (len ("-Dddd HH"      ))
		case T_INTERVAL_DAY_TO_MINUTE    : uMinUnscaledByteCount = uint64 (len ("-D HH:MM"   )) ; uMaxUnscaledByteCount = uint64 (len ("-Dddd HH:MM"   ))
		case T_INTERVAL_DAY_TO_SECOND    : uMinUnscaledByteCount = uint64 (len ("-D HH:MM:SS")) ; uMaxUnscaledByteCount = uint64 (len ("-Dddd HH:MM:SS")) ; bScaled = true
		case T_INTERVAL_HOUR             : uMinUnscaledByteCount = uint64 (len ("-H"         )) ; uMaxUnscaledByteCount = uint64 (len ("-Hhhh"         ))
		case T_INTERVAL_HOUR_TO_MINUTE   : uMinUnscaledByteCount = uint64 (len ("-H:MM"      )) ; uMaxUnscaledByteCount = uint64 (len ("-Hhhh:MM"      ))
		case T_INTERVAL_HOUR_TO_SECOND   : uMinUnscaledByteCount = uint64 (len ("-H:MM:SS"   )) ; uMaxUnscaledByteCount = uint64 (len ("-Hhhh:MM:SS"   )) ; bScaled = true
		case T_INTERVAL_MINUTE           : uMinUnscaledByteCount = uint64 (len ("-M"         )) ; uMaxUnscaledByteCount = uint64 (len ("-Mmmm"         ))
		case T_INTERVAL_MINUTE_TO_SECOND : uMinUnscaledByteCount = uint64 (len ("-M:SS"      )) ; uMaxUnscaledByteCount = uint64 (len ("-Mmmm:SS"      )) ; bScaled = true
		case T_INTERVAL_SECOND           : uMinUnscaledByteCount = uint64 (len ("-S"         )) ; uMaxUnscaledByteCount = uint64 (len ("-Ssss"         )) ; bScaled = true
	}

	if uMaxUnscaledByteCount == 0 {
		uMaxUnscaledByteCount = uMinUnscaledByteCount
	}

	if bScaled {
		uMinScaledByteCount = uMinUnscaledByteCount + 1 + 1
		uMaxScaledByteCount = uMaxUnscaledByteCount + 1 + 6
	}

	return

} // end func timeRanges

func timeScale (uCookedDataType uint16, uByteCount uint64) uint8 {

	switch uCookedDataType {

		case T_TIME                     ,
			 T_TIMESTAMP                ,
			 T_TIME_WITH_TIME_ZONE      ,
			 T_TIMESTAMP_WITH_TIME_ZONE :

			_, _, _, uMinScaledByteCount, _ := timeRanges (uCookedDataType)

			if uByteCount >= uMinScaledByteCount {
				return uint8 (uByteCount - uMinScaledByteCount + 1)
			}
	} // end switch

	return 0

} // end func timeScale

// This is a value receiver method in order to work with a value inside an interface.

func (val TeradataValue) Scale () uint8 {

	switch val.m_uCookedDataType {

		case T_DECIMAL                         ,
			 T_PERIOD_TIME                     ,
			 T_PERIOD_TIME_WITH_TIME_ZONE      ,
			 T_PERIOD_TIMESTAMP                ,
			 T_PERIOD_TIMESTAMP_WITH_TIME_ZONE :

			return val.m_uScale

		case T_TIME                     ,
			 T_TIMESTAMP                ,
			 T_TIME_WITH_TIME_ZONE      ,
			 T_TIMESTAMP_WITH_TIME_ZONE :

			return timeScale (val.m_uCookedDataType, val.ByteCount ())

		case T_INTERVAL_DAY_TO_SECOND    ,
			 T_INTERVAL_HOUR_TO_SECOND   ,
			 T_INTERVAL_MINUTE_TO_SECOND ,
			 T_INTERVAL_SECOND           :

			if ! val.m_bPresent {
				return val.m_uScale // use the specified scale for a null Interval...Second value
			}

			if iPeriod := bytes.IndexByte (val.m_abyValue, '.') ; iPeriod >= 0 {
				return uint8 (val.ByteCount () - uint64 (iPeriod) - 1)
			}
	} // end switch

	return 0

} // end method Scale

// This is a value receiver method in order to work with a value inside an interface.

func (val TeradataValue) TypeDesc () string {

	_, _, _, _, sTypeName := dataTypeInfo (val.m_uCookedDataType)

	return fmt.Sprintf ("%v(%v,%v %v,%v,%v)",
		reflect.TypeOf (val),
		func () string { if val.m_bPresent { return "Valid" } ; return "Null" } (),
		val.m_uCookedDataType,
		sTypeName,
		val.ByteCount (),
		val.m_uScale)

} // end method TypeDesc

func (val *TeradataValue) Bytes () (abyOutput [] byte, bOutput bool) {

	if ! val.m_bPresent { return }

	abyOutput = val.m_abyValue
	bOutput = true
	return

} // end method Bytes

func (val *TeradataValue) CopyBytes () (abyOutput [] byte, bOutput bool) {

	if ! val.m_bPresent { return }

	abyOutput = copyByteSlice (val.m_abyValue)
	bOutput = true
	return

} // end method CopyBytes

func (val *TeradataValue) AsTime () (o time.Time, bOutput bool) {

	if ! val.m_bPresent || val.m_uCookedDataType != T_TIMESTAMP_WITH_TIME_ZONE { return }

	// Excerpt from documentation for time.Parse:
	// When parsing (only), the input may contain a fractional second field immediately after the seconds field, even if the layout does not signify its presence.
	// In that case a decimal point followed by a maximal series of digits is parsed as a fractional second.

	o, err := time.Parse ("2006-01-02 15:04:05-07:00", string (val.m_abyValue))
	if err != nil { return }

	bOutput = true
	return

} // end method AsTime

func (val *TeradataValue) AsNullFloat64 () (o sql.NullFloat64, bOutput bool) {

	if val.m_uCookedDataType != T_FLOAT { return }

	o = sql.NullFloat64 { Valid : val.m_bPresent }

	if o.Valid { o.Float64 = math.Float64frombits (binary.BigEndian.Uint64 (val.m_abyValue)) }

	bOutput = true
	return

} // end method AsNullFloat64

func (val *TeradataValue) AsNullInt64 () (o sql.NullInt64, bOutput bool) {

	switch val.m_uCookedDataType {

		case T_BYTEINT, T_SMALLINT, T_INTEGER, T_BIGINT: // do nothing

		default: return
	}

	o = sql.NullInt64 { Valid : val.m_bPresent }

	if o.Valid {

		switch val.m_uCookedDataType {

			case T_BYTEINT  : o.Int64 = int64 (int8  (val.m_abyValue [0]))
			case T_SMALLINT : o.Int64 = int64 (int16 (binary.BigEndian.Uint16 (val.m_abyValue)))
			case T_INTEGER  : o.Int64 = int64 (int32 (binary.BigEndian.Uint32 (val.m_abyValue)))
			case T_BIGINT   : o.Int64 = int64 (       binary.BigEndian.Uint64 (val.m_abyValue))

		} // end switch
	} // end if

	bOutput = true
	return

} // end method AsNullInt64

func (val *TeradataValue) AsNullString () (o sql.NullString) {

	o = sql.NullString { Valid : val.m_bPresent }

	if o.Valid { o.String = val.String () }

	return

} // end method AsNullString

// Values returned by outboundType
var T_byteSlice     = reflect.TypeOf ((*[] byte      ) (nil)).Elem ()
var T_float64       = reflect.TypeOf ((*float64      ) (nil)).Elem ()
var T_int8          = reflect.TypeOf ((*int8         ) (nil)).Elem ()
var T_int16         = reflect.TypeOf ((*int16        ) (nil)).Elem ()
var T_int32         = reflect.TypeOf ((*int32        ) (nil)).Elem ()
var T_int64         = reflect.TypeOf ((*int64        ) (nil)).Elem ()
var T_string        = reflect.TypeOf ((*string       ) (nil)).Elem ()
var T_TeradataValue = reflect.TypeOf ((*TeradataValue) (nil)).Elem ()

func GoTypeFromDatabaseTypeName (s string) reflect.Type {

	switch s {

		case "BLOB"                        : return T_byteSlice
		case "LOCATOR(BLOB)"               : return T_byteSlice
		case "LOCATOR(CLOB)"               : return T_byteSlice
		case "VARBYTE"                     : return T_byteSlice
		case "BYTE"                        : return T_byteSlice
		case "LONG VARBYTE"                : return T_byteSlice
		case "FLOAT"                       : return T_float64
		case "BYTEINT"                     : return T_int8
		case "SMALLINT"                    : return T_int16
		case "INTEGER"                     : return T_int32
		case "BIGINT"                      : return T_int64
		case "LOCATOR(XML)"                : return T_byteSlice
		case "LOCATOR(JSON)"               : return T_byteSlice
		case "DATASET STORAGE FORMAT AVRO" : return T_byteSlice
		case "DATASET STORAGE FORMAT CSV"  : return T_byteSlice
		default                            : return T_string
	}
} // end GoTypeFromDatabaseTypeName

func outboundType (bTeradataValues bool, uCookedDataType uint16) reflect.Type {

	if bTeradataValues { return T_TeradataValue }

	switch uCookedDataType {

		case T_BLOB_AS_INLINE  : return T_byteSlice
		case T_BLOB_AS_LOCATOR : return T_byteSlice
		case T_CLOB_AS_LOCATOR : return T_byteSlice
		case T_VARBYTE         : return T_byteSlice
		case T_BYTE            : return T_byteSlice
		case T_LONG_VARBYTE    : return T_byteSlice
		case T_FLOAT           : return T_float64
		case T_BYTEINT         : return T_int8
		case T_SMALLINT        : return T_int16
		case T_INTEGER         : return T_int32
		case T_BIGINT          : return T_int64
		case T_XML_AS_LOCATOR  : return T_byteSlice
		case T_JSON_AS_LOCATOR : return T_byteSlice
		default                : return T_string
	}
} // end outboundType

func (val *TeradataValue) AsGoType () any {

	if ! val.m_bPresent { return nil }

	switch val.m_uCookedDataType {

		case T_BLOB_AS_INLINE  : return val.m_abyValue
		case T_BLOB_AS_LOCATOR : return val.m_abyValue
		case T_CLOB_AS_LOCATOR : return val.m_abyValue
		case T_VARBYTE         : return val.m_abyValue
		case T_BYTE            : return val.m_abyValue
		case T_LONG_VARBYTE    : return val.m_abyValue
		case T_FLOAT           : return math.Float64frombits (binary.BigEndian.Uint64 (val.m_abyValue))
		case T_BYTEINT         : return int8  (val.m_abyValue [0])
		case T_SMALLINT        : return int16 (binary.BigEndian.Uint16 (val.m_abyValue))
		case T_INTEGER         : return int32 (binary.BigEndian.Uint32 (val.m_abyValue))
		case T_BIGINT          : return int64 (binary.BigEndian.Uint64 (val.m_abyValue))
		case T_XML_AS_LOCATOR  : return val.m_abyValue
		case T_JSON_AS_LOCATOR : return val.m_abyValue
		default                : return val.String ()
	}
} // end method AsGoType

func tooManyDecimalDigits (s string) bool {

	n := len (s)
	if n > 0 && s [0] == '-' { n-- } // ignore leading minus sign
	return n > 38

} // end tooManyDecimalDigits

func (val *TeradataValue) adjustDecimalScale (uDesiredPrecision uint8, uDesiredScale uint8) {

	if ! val.m_bPresent {
		return
	}

	nScaleDelta := int (uDesiredScale - val.Scale ()) // assumes that uDesiredScale is always larger
	bi := convertByteSliceToSignedBigInt (val.m_abyValue)

	for n := 0 ; n < nScaleDelta ; n++ {
		bi.Mul (&bi, BIG_TEN)
	}

	if tooManyDecimalDigits (bi.String ()) {
		val.m_err = formatError (nil, "Desired scale %v is too large for decimal value %v", uDesiredScale, val)
		return
	}

	aby := convertBigIntToByteSlice (&bi)
	val.m_abyValue = adjustDecimalLength (aby, uDesiredPrecision, uDesiredScale)
	val.m_uScale = uDesiredScale

} // end adjustDecimalScale

func adjustDecimalLength (aby [] byte, uPrecision uint8, uScale uint8) [] byte {

	// Ensure that the byte slice is 1, 2, 4, 8, or 16 bytes long. Pad with leading sign bytes if needed.

	uPrecision      = maxUint8 (uPrecision, uScale)
	nNeedByteCount := int (byteCountFromDecimalPrecision (uPrecision))
	bNegative      := aby [0] & 0x80 != 0
	byLead         := func () byte { if bNegative { return 0xFF } ; return 0 } ()

	for len (aby) < nNeedByteCount {
		aby = append ([] byte { byLead }, aby ...)
	}

	return aby

} // end adjustDecimalLength

func (val TeradataValue) adjustByteLength (con *TeradataConnection, uByteCount uint64, uPrecision uint8) TeradataValue {

	if ! val.m_bPresent {
		val.m_uByteCount = uByteCount
		return val
	}

	switch val.m_uCookedDataType {

		case T_CHAR                      ,
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

			val.m_abyValue = con.fixedWidthInSessionCharset (val.m_abyValue, uByteCount)

		case T_BYTE:

			val.m_abyValue = padByteSlice (val.m_abyValue, uByteCount, /*byPad*/ 0)

		case T_DECIMAL:

			val.m_abyValue = adjustDecimalLength (val.m_abyValue, uPrecision, val.m_uScale)

	} // end switch

	return val

} // end adjustByteLength

func (val TeradataValue) DebugString () string {

	_, _, _, _, sTypeName := dataTypeInfo (val.m_uCookedDataType)

	return fmt.Sprintf ("%v,%v,%v,%v,%v [%v] = %v",
		func () string { if val.m_bPresent { return "Pres" } ; return "Null" } (),
		val.m_uCookedDataType,
		sTypeName,
		val.ByteCount (),
		val.m_uScale,
		hex.EncodeToString (val.m_abyValue),
		val)

} // end DebugString

// You cannot obtain a pointer to what is stored inside an interface value because that address may become a different type if something else is assigned to the interface.
// Value-receiver methods can be invoked on pointers or values, but pointer-receiver methods can only be invoked on pointers.
// Therefore, an implementation of the fmt.Stringer String method must have a value receiver in order to work with a value inside an interface.

func (val TeradataValue) String () string { // satisfies fmt.Stringer

	if ! val.m_bPresent { return "NULL" }

	switch val.m_uCookedDataType {

		case // character types
			T_CLOB_AS_INLINE            ,
			T_VARCHAR                   ,
			T_CHAR                      ,
			T_LONG_VARCHAR              ,
			T_XML                       ,
			T_JSON                      ,
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

			return string (val.m_abyValue)

		case T_DATE_INTEGER:

			nYear, nMonth, nDay := shredIntegerDate (val.m_abyValue)
			return fmt.Sprintf ("%04d-%02d-%02d", nYear, nMonth, nDay)

		case T_BYTEINT:

			return fmt.Sprintf ("%v", int8 (val.m_abyValue [0]))

		case T_SMALLINT:

			return fmt.Sprintf ("%v", int16 (binary.BigEndian.Uint16 (val.m_abyValue)))

		case T_INTEGER:

			return fmt.Sprintf ("%v", int32 (binary.BigEndian.Uint32 (val.m_abyValue)))

		case T_BIGINT:

			return fmt.Sprintf ("%v", int64 (binary.BigEndian.Uint64 (val.m_abyValue)))

		case T_FLOAT:

			return fmt.Sprintf ("%v", math.Float64frombits (binary.BigEndian.Uint64 (val.m_abyValue)))

		case T_DECIMAL:

			return formatBigIntWithScale (convertByteSliceToSignedBigInt (val.m_abyValue), int (val.m_uScale))

		case T_NUMBER:

			switch {
				case len (val.m_abyValue) == 0:
					return "0"

				default:
					// 2 byte signed scale followed by 1 to 17 bytes unscaled value
					nScale := int (int16 (binary.BigEndian.Uint16 (val.m_abyValue)))
					return formatBigIntWithScale (convertByteSliceToSignedBigInt (val.m_abyValue [2 : ]), nScale)
			}

		case T_PERIOD_DATE:

			nBegYear, nBegMonth, nBegDay := shredIntegerDate (val.m_abyValue)
			nEndYear, nEndMonth, nEndDay := shredIntegerDate (val.m_abyValue [4 : ])

			return fmt.Sprintf ("%04d-%02d-%02d,%04d-%02d-%02d",
				nBegYear, nBegMonth, nBegDay,
				nEndYear, nEndMonth, nEndDay)

		case T_PERIOD_TIME:

			nBegHour, nBegMinute, nBegSecond, nBegFracDigits := shredBinaryTime (val.m_abyValue)
			nEndHour, nEndMinute, nEndSecond, nEndFracDigits := shredBinaryTime (val.m_abyValue [6 : ])

			if val.m_uScale == 0 {
				return fmt.Sprintf ("%02d:%02d:%02d,%02d:%02d:%02d",
					nBegHour, nBegMinute, nBegSecond,
					nEndHour, nEndMinute, nEndSecond)
			} else {
				return fmt.Sprintf ("%02d:%02d:%02d.%06d,%02d:%02d:%02d.%06d",
					nBegHour, nBegMinute, nBegSecond, nBegFracDigits,
					nEndHour, nEndMinute, nEndSecond, nEndFracDigits)
			}

		case T_PERIOD_TIME_WITH_TIME_ZONE:

			nBegHour, nBegMinute, nBegSecond, nBegFracDigits := shredBinaryTime (val.m_abyValue)
			nEndHour, nEndMinute, nEndSecond, nEndFracDigits := shredBinaryTime (val.m_abyValue [8 : ])

			nBegTZHour, nBegTZMinute := shredBinaryTimeZone (val.m_abyValue [ 6 : ])
			nEndTZHour, nEndTZMinute := shredBinaryTimeZone (val.m_abyValue [14 : ])

			if val.m_uScale == 0 {
				return fmt.Sprintf ("%02d:%02d:%02d%+03d:%02d,%02d:%02d:%02d%+03d:%02d",
					nBegHour, nBegMinute, nBegSecond, nBegTZHour, nBegTZMinute,
					nEndHour, nEndMinute, nEndSecond, nEndTZHour, nEndTZMinute)
			} else {
				return fmt.Sprintf ("%02d:%02d:%02d.%06d%+03d:%02d,%02d:%02d:%02d.%06d%+03d:%02d",
					nBegHour, nBegMinute, nBegSecond, nBegFracDigits, nBegTZHour, nBegTZMinute,
					nEndHour, nEndMinute, nEndSecond, nEndFracDigits, nEndTZHour, nEndTZMinute)
			}

		case T_PERIOD_TIMESTAMP:

			nBegYear, nBegMonth, nBegDay, nBegHour, nBegMinute, nBegSecond, nBegFracDigits := shredBinaryTimestamp (val.m_abyValue)
			nEndYear, nEndMonth, nEndDay, nEndHour, nEndMinute, nEndSecond, nEndFracDigits := shredBinaryTimestamp (val.m_abyValue [10 : ])

			if val.m_uScale == 0 {
				return fmt.Sprintf ("%04d-%02d-%02d %02d:%02d:%02d,%04d-%02d-%02d %02d:%02d:%02d",
					nBegYear, nBegMonth, nBegDay, nBegHour, nBegMinute, nBegSecond,
					nEndYear, nEndMonth, nEndDay, nEndHour, nEndMinute, nEndSecond)
			} else {
				return fmt.Sprintf ("%04d-%02d-%02d %02d:%02d:%02d.%06d,%04d-%02d-%02d %02d:%02d:%02d.%06d",
					nBegYear, nBegMonth, nBegDay, nBegHour, nBegMinute, nBegSecond, nBegFracDigits,
					nEndYear, nEndMonth, nEndDay, nEndHour, nEndMinute, nEndSecond, nEndFracDigits)
			}

		case T_PERIOD_TIMESTAMP_WITH_TIME_ZONE:

			nBegYear, nBegMonth, nBegDay, nBegHour, nBegMinute, nBegSecond, nBegFracDigits := shredBinaryTimestamp (val.m_abyValue)
			nEndYear, nEndMonth, nEndDay, nEndHour, nEndMinute, nEndSecond, nEndFracDigits := shredBinaryTimestamp (val.m_abyValue [12 : ])

			nBegTZHour, nBegTZMinute := shredBinaryTimeZone (val.m_abyValue [10 : ])
			nEndTZHour, nEndTZMinute := shredBinaryTimeZone (val.m_abyValue [22 : ])

			if val.m_uScale == 0 {
				return fmt.Sprintf ("%04d-%02d-%02d %02d:%02d:%02d%+03d:%02d,%04d-%02d-%02d %02d:%02d:%02d%+03d:%02d",
					nBegYear, nBegMonth, nBegDay, nBegHour, nBegMinute, nBegSecond, nBegTZHour, nBegTZMinute,
					nEndYear, nEndMonth, nEndDay, nEndHour, nEndMinute, nEndSecond, nEndTZHour, nEndTZMinute)
			} else {
				return fmt.Sprintf ("%04d-%02d-%02d %02d:%02d:%02d.%06d%+03d:%02d,%04d-%02d-%02d %02d:%02d:%02d.%06d%+03d:%02d",
					nBegYear, nBegMonth, nBegDay, nBegHour, nBegMinute, nBegSecond, nBegFracDigits, nBegTZHour, nBegTZMinute,
					nEndYear, nEndMonth, nEndDay, nEndHour, nEndMinute, nEndSecond, nEndFracDigits, nEndTZHour, nEndTZMinute)
			}

		default:

			return hex.EncodeToString (val.m_abyValue)

	} // end switch

} // end method String

func timeByteCount (uMinByteCount uint64, uScale uint8) uint64 {

	if uScale == 0 { return uMinByteCount }

	return uMinByteCount + /*decimal point*/ 1 + uint64 (uScale)

} // end func timeByteCount

func shredIntegerDate (aby [] byte) (nYear, nMonth, nDay int32) {

	nIntegerDate := int32 (binary.BigEndian.Uint32 (aby)) + 19000000
	nYear         = nIntegerDate / 10000
	nMonth        = (nIntegerDate % 10000) / 100
	nDay          = nIntegerDate % 100
	return

} // end func shredIntegerDate

func parseIntegerDate (s string) ([] byte, error) {

	t, err := time.Parse ("2006-01-02", s)

	if err != nil {
		return nil, formatError (err, "Invalid date value %v", s)
	}

	nIntegerDate := t.Year () * 10000 + int (t.Month ()) * 100 + t.Day () - 19000000

	return fourBytes (uint32 (nIntegerDate)), nil

} // end func parseIntegerDate

func shredBinaryTime (aby [] byte) (nHour, nMinute, nSecond, nFracDigits int32) {

	nScaledSecs := int32 (binary.BigEndian.Uint32 (aby))
	nHour        = int32 (aby [4])
	nMinute      = int32 (aby [5])
	nSecond      = nScaledSecs / 1000000
	nFracDigits  = nScaledSecs % 1000000
	return

} // end func shredBinaryTime

func parseBinaryTime (s string) ([] byte, error) {

	// Excerpt from documentation for time.Parse:
	// When parsing (only), the input may contain a fractional second field immediately after the seconds field, even if the layout does not signify its presence.
	// In that case a decimal point followed by a maximal series of digits is parsed as a fractional second.

	t, err := time.Parse ("15:04:05", s)

	if err != nil {
		return nil, formatError (err, "Invalid time value %v", s)
	}

	nScaledSecs := t.Second () * 1000000 + t.Nanosecond () / 1000

	return append (fourBytes (uint32 (nScaledSecs)), byte (t.Hour ()), byte (t.Minute ())), nil

} // end func parseBinaryTime

func shredBinaryTimeZone (aby [] byte) (nHour, nMinute int32) {

	nHour   = int32 (aby [0])
	nMinute = int32 (aby [1])

	if nHour < 16 {
		nHour = -nHour
	} else {
		nHour -= 16
	}

	return

} // end func shredBinaryTimeZone

func parseBinaryTimeZone (sInput string) (sLead string, abyTimeZone [] byte, err error) {

	iSign := len (sInput) - 6

	if iSign < 0 || sInput [iSign] != '+' && sInput [iSign] != '-' || sInput [iSign + 3] != ':' {

		err = formatError (nil, "Missing time zone %v", sInput)
		return
	}

	uHour, err := parse10Uint8 (sInput [iSign + 1 : iSign + 3])
	if err != nil {
		err = formatError (err, "Invalid time zone hour %v", sInput)
		return
	}

	uMinute, err := parse10Uint8 (sInput [iSign + 4 : ])
	if err != nil {
		err = formatError (err, "Invalid time zone minute %v", sInput)
		return
	}

	if sInput [iSign] == '+' { uHour += 16 }

	sLead = sInput [ : iSign]
	abyTimeZone = [] byte { uHour, uMinute }
	return

} // end func parseBinaryTimeZone

func parseBinaryTimeWithTimeZone (s string) ([] byte, error) {

	sLead, abyTimeZone, err := parseBinaryTimeZone (s)
	if err != nil {
		return nil, err
	}

	abyLead, err := parseBinaryTime (sLead)
	if err != nil {
		return nil, err
	}

	return append (abyLead, abyTimeZone ...), nil

} // end func parseBinaryTimeWithTimeZone

func shredBinaryTimestamp (aby [] byte) (nYear, nMonth, nDay, nHour, nMinute, nSecond, nFracDigits int32) {

	nScaledSecs := int32 (binary.BigEndian.Uint32 (aby))
	nYear        = int32 (int16 (binary.BigEndian.Uint16 (aby [4 : ])))
	nMonth       = int32 (aby [6])
	nDay         = int32 (aby [7])
	nHour        = int32 (aby [8])
	nMinute      = int32 (aby [9])
	nSecond      = nScaledSecs / 1000000
	nFracDigits  = nScaledSecs % 1000000
	return

} // end func shredBinaryTimestamp

func parseBinaryTimestamp (s string) ([] byte, error) {

	t, err := time.Parse ("2006-01-02 15:04:05", s)

	if err != nil {
		return nil, formatError (err, "Invalid timestamp value %v", s)
	}

	nScaledSecs := t.Second () * 1000000 + t.Nanosecond () / 1000

	return append (append (
		fourBytes (uint32 (nScaledSecs)),
		twoBytes (uint16 (t.Year ())) ...),
		uint8 (t.Month ()),
		uint8 (t.Day ()),
		uint8 (t.Hour ()),
		uint8 (t.Minute ())), nil

} // end func parseBinaryTimestamp

func parseBinaryTimestampWithTimeZone (s string) ([] byte, error) {

	sLead, abyTimeZone, err := parseBinaryTimeZone (s)
	if err != nil {
		return nil, err
	}

	abyLead, err := parseBinaryTimestamp (sLead)
	if err != nil {
		return nil, err
	}

	return append (abyLead, abyTimeZone ...), nil

} // end func parseBinaryTimestampWithTimeZone

func formatBigIntWithScale (n big.Int, nScale int) string {

	sValue := n.String ()

	sSign := ""
	if sValue [0] == '-' {
		sSign = "-"
		sValue = sValue [1 : ]
	}

	switch {
		case nScale < 0:
			sValue = sValue + strings.Repeat ("0", -nScale)

		case nScale >= len (sValue):
			sValue = "." + strings.Repeat ("0", nScale - len (sValue)) + sValue

		case nScale > 0:
			sValue = sValue [ : len (sValue) - nScale] + "." + sValue [len (sValue) - nScale : ]
	}

	return sSign + sValue

} // end func formatBigIntWithScale

func convertByteSliceToUnsignedBigInt (aby [] byte) (u big.Int) {

	u.SetBytes (aby)
	return

} // end func convertByteSliceToUnsignedBigInt

func convertByteSliceToSignedBigInt (aby [] byte) (n big.Int) {

	n.SetInt64 (int64 (int8 (aby [0]))) // take sign from first byte
	n.Lsh (&n, uint ((len (aby) - 1) * 8)) // shift first byte into its proper position

	u := convertByteSliceToUnsignedBigInt (aby [1 : ]) // create unsigned big.Int from the rest of the bytes

	n.Or (&n, &u) // bitwise-or the unsigned rest of the bytes into the signed big.Int
	return

} // end func convertByteSliceToSignedBigInt

func convertBigIntToByteSlice (pn *big.Int) [] byte {

	pn = new (big.Int).Set (pn)  // make a deep copy

	bNegative := pn.Sign () < 0

	// Convert negative numbers to their two's-complement form per this formula: bitwise_not(abs(n + 1))
	if bNegative {
		pn.Add (pn, BIG_ONE)
	}

	// The big.Int.Bytes method produces the bits of the absolute value and also produces a zero-length slice if the value is zero.
	aby := pn.Bytes ()

	// Convert a zero-length slice into the appropriate slice.
	if len (aby) == 0 {
		aby = [] byte { 0 }
	}

	// Flip all the bits (bitwise not) if the value is supposed to be negative.
	for i := 0 ; bNegative && i < len (aby) ; i++ {
		aby [i] ^= 0xFF
	}

	// Ensure that the first bit carries the appropriate sign. Prepend a byte of all sign bits if the first bit is wrong.
	bHasNegativeBit := aby [0] & 0x80 != 0
	bNeedsLeadByte  := bNegative != bHasNegativeBit

	if bNeedsLeadByte {
		byLead := func () byte { if bNegative { return 0xFF } ; return 0 } ()
		aby     = append ([] byte { byLead }, aby ...)
	}

	return aby

} // end func convertBigIntToByteSlice

func parseDecimal (sInput string, bPowerOf2Length bool) (uScale uint16, abyOutput [] byte, err error) {

	as := strings.Split (sInput, ".")
	if len (as) > 2 {
		err = formatError (nil, "Value must contain one decimal point at most: %v", sInput)
		return
	}

	// If no decimal point, then add an empty string for the fractional digits.
	if len (as) == 1 {
		as = append (as, "")
	}

	nFracDigitsCount := len (as [1])
	if nFracDigitsCount > 0xFF {
		err = formatError (nil, "Invalid decimal value %v", sInput)
		return
	}

	sUnscaled := as [0] + as [1]

	iEnd := len (sUnscaled)
	for ! bPowerOf2Length && iEnd > 1 && sUnscaled [iEnd - 1] == '0' { // Identify trailing zeros of Number.
		iEnd--
	}

	nTrimCount := len (sUnscaled) - iEnd // Will be non-negative for Number. Will always be zero for Decimal.
	nFracDigitsCount -= nTrimCount // May produce negative scale for Number. Will do nothing for Decimal.
	sUnscaled = sUnscaled [ : iEnd] // Remove trailing zeros of Number. Will do nothing for Decimal.

	if bPowerOf2Length && tooManyDecimalDigits (sUnscaled) {
		err = formatError (nil, "Decimal value is too large: %v", sInput)
		return
	}

	var n big.Int
	_, bParsed := n.SetString (sUnscaled, /*base*/ 10)

	if ! bParsed {
		err = formatError (nil, "Invalid decimal value %v", sInput)
		return
	}

	aby := convertBigIntToByteSlice (&n)

	if bPowerOf2Length { // Ensure that the byte slice is 1, 2, 4, 8, or 16 bytes long. Pad with leading sign bytes if needed.

		uPrecision     := uint8 (maxInt (bigIntDigitCount (&n), nFracDigitsCount))
		nNeedByteCount := int (byteCountFromDecimalPrecision (uPrecision))
		bNegative      := aby [0] & 0x80 != 0
		byLead         := func () byte { if bNegative { return 0xFF } ; return 0 } ()

		for len (aby) < nNeedByteCount {
			aby = append ([] byte { byLead }, aby ...)
		}
	} // end if bPowerOf2Length

	uScale    = uint16 (nFracDigitsCount) // previously verified <= 0xFF
	abyOutput = aby
	return

} // end parseDecimal
