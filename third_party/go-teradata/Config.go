// Copyright 2017 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"fmt"
	"strings"
)

type vproc struct {
	M_uVProcNumber                      uint16       `json:"Number"`
	M_uVProcState                       uint8        `json:"State"`
}

type charset struct {
	M_sCharsetName                      string       `json:"Name"`
	M_uCharsetCode                      uint8        `json:"Code"`
	m_uInternationalHash                uint8
}

// This structure is marshalled into JSON, and therefore it cannot contain any [] byte or [] uint8 fields
// because those data types are marshalled as base64 encoded strings, which are not human-readable.

type config struct {

	M_uFirstSessionNumber               uint32       `json:"FirstSessionNumber"`
	M_uFirstRequestNumber               uint32       `json:"FirstRequestNumber"`
	M_uMaxSessionCount                  uint16       `json:"MaxSessionCount"`
	M_uIFPCount                         uint16       `json:"IFPCount"`
	M_aVProcs                           [] vproc     `json:"VProcs"`
	M_uAMPCount                         uint16       `json:"AMPCount"`
	M_auAMPNumbers                      [] uint16    `json:"AMPNumbers"`
	M_uDefaultCharsetCode               uint8        `json:"DefaultCharsetCode"`
	M_uCharsetCount                     uint16       `json:"CharsetCount"`
	M_aCharsets                         [] charset   `json:"Charsets"`
	M_sInDoubtSessionsExist             string       `json:"InDoubtSessionsExist"`
	m_uExtensionAreaFollows             uint8
	M_sDefaultTransactionSemantics      string       `json:"DefaultTransactionSemantics"`
	M_uANSIDateTimeSupport              uint8        `json:"ANSIDateTimeSupport"`
	M_sDefaultDateForm                  string       `json:"DefaultDateForm"`
	M_sDefaultRowSize                   string       `json:"DefaultRowSize"`
	M_uMaxMultiTSRSegmentCount          uint16       `json:"MaxMultiTSRSegmentCount"`
	M_uMaxMultiTSRSegmentSize           uint32       `json:"MaxMultiTSRSegmentSize"`
	M_uMaxRowSizeInBytes                uint32       `json:"MaxRowSizeInBytes"`
	M_uMaxLOBSizeInBytes                uint64       `json:"MaxLOBSizeInBytes"`
	M_uMaxRequestSizeInBytes            uint32       `json:"MaxRequestSizeInBytes"`
	M_uMaxResponseSizeInBytes           uint32       `json:"MaxResponseSizeInBytes"`
	M_uMaxSPParameterCount              uint32       `json:"MaxSPParameterCount"`
	M_uMaxColumnsInGroupBy              uint32       `json:"MaxColumnsInGroupBy"`
	M_uMaxColumnsInOrderBy              uint32       `json:"MaxColumnsInOrderBy"`
	M_uMaxColumnsInSelect               uint32       `json:"MaxColumnsInSelect"`
	M_uMaxColumnsInTable                uint32       `json:"MaxColumnsInTable"`
	M_uMaxObjectNameLengthInChars       uint32       `json:"MaxObjectNameLengthInChars"`
	M_uMaxTablesInSelect                uint32       `json:"MaxTablesInSelect"`
	M_uMaxUsingFieldCount               uint32       `json:"MaxUsingFieldCount"`
	M_uMaxUsingDataSizeInBytes          uint32       `json:"MaxUsingDataSizeInBytes"`
	M_uMaxByteLiteralLengthInChars      uint32       `json:"MaxByteLiteralLengthInChars"`
	M_uMaxCharLiteralLengthInChars      uint32       `json:"MaxCharLiteralLengthInChars"`
	M_uMaxDecimalPrecision              uint32       `json:"MaxDecimalPrecision"`
	M_uMaxColumnDataSizeInBytes         uint32       `json:"MaxColumnDataSizeInBytes"`
	M_uMaxVarcharLengthInChars          uint32       `json:"MaxVarcharLengthInChars"`
	M_uMaxCharLengthInChars             uint32       `json:"MaxCharLengthInChars"`
	M_uMaxGraphicLengthInChars          uint32       `json:"MaxGraphicLengthInChars"`
	M_uMaxVargraphicLengthInChars       uint32       `json:"MaxVargraphicLengthInChars"`
	M_uMaxVarbyteSizeInBytes            uint32       `json:"MaxVarbyteSizeInBytes"`
	M_uMaxByteSizeInBytes               uint32       `json:"MaxByteSizeInBytes"`
	M_uMaxParameterCountInRequest       uint32       `json:"MaxParameterCountInRequest"`
	M_uMaxTimeScale                     uint32       `json:"MaxTimeScale"`
	M_uMaxTimestampScale                uint32       `json:"MaxTimestampScale"`
	M_uMaxIntervalToSecondScale         uint32       `json:"MaxIntervalToSecondScale"`
	M_uMaxDeferredByNameFileNameBytes   uint32       `json:"MaxDeferredByNameFileNameBytes"`
	M_uMaxAvailableBytesInResponseRow   uint32       `json:"MaxAvailableBytesInResponseRow"`
	M_uMaxJsonByteCount                 uint32       `json:"MaxJsonByteCount"`
	M_uMaxRequestTextByteCount          uint32       `json:"MaxRequestTextByteCount"`
	M_uMaxRecordCountInIteratedRequest  uint32       `json:"MaxRecordCountInIteratedRequest"`
	M_uSSOSupported                     uint8        `json:"SSOSupported"`
	M_uAtomicUpsertSupport              uint8        `json:"AtomicUpsertSupport"`
	M_uDMLArraySupport                  uint8        `json:"DMLArraySupport"`
	M_uMergeIntoSupport                 uint8        `json:"MergeIntoSupport"`
	M_uOUTParamArgMarkerSupport         uint8        `json:"OUTParamArgMarkerSupport"`
	M_uLargeDecimalAndBigintSupport     uint8        `json:"LargeDecimalAndBigintSupport"`
	M_uAGKRSupport                      uint8        `json:"AGKRSupport"`
	M_uQueryBandSupport                 uint8        `json:"QueryBandSupport"`
	M_uFullANSIMergeSupport             uint8        `json:"FullANSIMergeSupport"`
	M_uLoggingErrorsSupport             uint8        `json:"LoggingErrorsSupport"`
	M_uColumnCorrelationSupport         uint8        `json:"ColumnCorrelationSupport"`
	M_uTemporalSemanticsSupport         uint8        `json:"TemporalSemanticsSupport"`
	M_uQueryBandProxyUserSupport        uint8        `json:"QueryBandProxyUserSupport"`
	M_uUserDefinedIndexSupport          uint8        `json:"UserDefinedIndexSupport"`
	M_uTrustedSQLRequestSupport         uint8        `json:"TrustedSQLRequestSupport"`
	M_uMandatoryAccessControlSupport    uint8        `json:"MandatoryAccessControlSupport"`
	M_uUserDefinedOperatorSupport       uint8        `json:"UserDefinedOperatorSupport"`
	M_uXMLDataTypeAndXQuerySupport      uint8        `json:"XMLDataTypeAndXQuerySupport"`
	M_uTemporalEnabledLevel             uint8        `json:"TemporalEnabledLevel"`
	M_uPartitionLevelLockingSupport     uint8        `json:"PartitionLevelLockingSupport"`
	M_uBooleanDataTypeSupport           uint8        `json:"BooleanDataTypeSupport"`
	M_uJSONDataTypeSupport              uint8        `json:"JSONDataTypeSupport"`
	M_uSecureZonesSupport               uint8        `json:"SecureZonesSupport"`
	M_uUnicodePassThroughSupport        uint8        `json:"UnicodePassThroughSupport"`
	M_uAvroDatasetDataTypeSupport       uint8        `json:"AvroDatasetDataTypeSupport"`
	M_uCSVDatasetDataTypeSupport        uint8        `json:"CSVDatasetDataTypeSupport"`
	M_uTransformGroupForType            uint8        `json:"TransformGroupForType"`
	M_uODBCScalarFunctions              uint8        `json:"ODBCScalarFunctions"`
	M_uOTFSupport                       uint8        `json:"OTFSupport"`
	M_uVSDSupport                       uint8        `json:"VSDSupport"`
	M_uMultipartParcelAndLOBSupport     uint8        `json:"MultipartParcelAndLOBSupport"`
	M_uAPHRequestSupport                uint8        `json:"APHRequestSupport"`
	M_u1MBExtendedResponseSupport       uint8        `json:"1MBExtendedResponseSupport"`
	M_uSSRCursorPositioningSupport      uint8        `json:"SSRCursorPositioningSupport"`
	M_uUDTSupport                       uint8        `json:"UDTSupport"`
	M_uAPHResponseSupport               uint8        `json:"APHResponseSupport"`
	M_uSIPResponseSupport               uint8        `json:"SIPResponseSupport"`
	M_uIncreasedHashBucketsSupport      uint8        `json:"IncreasedHashBucketsSupport"`
	M_uSPDynamicResultSetsSupport       uint8        `json:"SPDynamicResultSetsSupport"`
	M_uMSRCursorPositioningSupport      uint8        `json:"MSRCursorPositioningSupport"`
	M_uSIPRequestSupport                uint8        `json:"SIPRequestSupport"`
	M_uStructSupport                    uint8        `json:"StructSupport"`
	M_uPeriodDataTypeSupport            uint8        `json:"PeriodDataTypeSupport"`
	M_uElicitByNameSupport              uint8        `json:"ElicitByNameSupport"`
	M_uClientAttributesParcelSupport    uint8        `json:"ClientAttributesParcelSupport"`
	M_uFetchRowCountParcelSupport       uint8        `json:"FetchRowCountParcelSupport"`
	M_uStatementIndependenceSupport     uint8        `json:"StatementIndependenceSupport"`
	M_uRNPSupport                       uint8        `json:"RNPSupport"`
	M_uArrayDataTypeSupport             uint8        `json:"ArrayDataTypeSupport"`
	M_uRowDataTypeSupport               uint8        `json:"RowDataTypeSupport"`
	M_uRedriveSupport                   uint8        `json:"RedriveSupport"`
	M_uControlDataSupport               uint8        `json:"ControlDataSupport"`
	M_uOverrideGeneratedValuesSupport   uint8        `json:"OverrideGeneratedValuesSupport"`
	M_uNumberDataTypeSupport            uint8        `json:"NumberDataTypeSupport"`
	M_uEnhancedStatementStatusSupport   uint8        `json:"EnhancedStatementStatusSupport"`
	M_uTDWMFailFastSupport              uint8        `json:"TDWMFailFastSupport"`
	M_uUnityClientAttributesSupport     uint8        `json:"UnityClientAttributesSupport"`
	M_uTDWMUnitySupport                 uint8        `json:"TDWMUnitySupport"`
	M_uUnityStoredProcedureSupport      uint8        `json:"UnityStoredProcedureSupport"`
	M_uSLOBClientToServerSupport        uint8        `json:"SLOBClientToServerSupport"`
	M_uSLOBServerToClientSupport        uint8        `json:"SLOBServerToClientSupport"`
	M_uUnityCDCSupport                  uint8        `json:"UnityCDCSupport"`
	M_uLoadIdentityColumnSupport        uint8        `json:"LoadIdentityColumnSupport"`
	M_uFastExportNoSpoolSupport         uint8        `json:"FastExportNoSpoolSupport"`
	M_uCheckWorkloadSupport             uint8        `json:"CheckWorkloadSupport"`
	M_uMLoadXProtocolSupport            uint8        `json:"MLoadXProtocolSupport"`
	M_uMonitorPartitionAsyncAbort       uint8        `json:"MonitorPartitionAsyncAbort"`
	M_uMultipleTransformSupport         uint8        `json:"MultipleTransformSupport"`
	M_sPDEVersion                       string       `json:"PDEVersion"`
	M_sDatabaseVersion                  string       `json:"DatabaseVersion"`
	M_uDatabaseVersionNumber            uint32       `json:"DatabaseVersionNumber"`
	M_uExtendedObjectNamesOnDisk        uint8        `json:"ExtendedObjectNamesOnDisk"`
	M_uExtendedObjectNamesInViews       uint8        `json:"ExtendedObjectNamesInViews"`
	M_uExtendedObjectNameProcessing     uint8        `json:"ExtendedObjectNameProcessing"`
	M_uExtendedObjectNamesInParcels     uint8        `json:"ExtendedObjectNamesInParcels"`

} // end type config

type gtwConfig struct {

	M_uSSOSupport                 uint8   `json:"SSOSupport"`                  // Gateway SSO support
	m_abyTDGSSVersion             [] byte                                      // TDGSS version (binary form)
	M_sTDGSSVersion               string  `json:"TDGSSVersion"`                // TDGSS version (string form)
	M_bUTF16Credentials           bool    `json:"UTF16Credentials"`            // UTF-16 Credentials Support
	M_uGatewayNodeId              uint16  `json:"GatewayNodeId"`               // Gateway Node ID
	M_bRecoverableProtocol        bool    `json:"RecoverableProtocol"`         // Recoverable Network Protocol
	M_bControlData                bool    `json:"ControlData"`                 // Control Data
	M_bRedrive                    bool    `json:"Redrive"`                     // Redrive
	M_uSecurityPolicy             uint8   `json:"SecurityPolicy"`              // Security Policy Support
	M_uNegoMech                   uint8   `json:"NegoMech"`                    // Negotiation Mechanism Support
	M_sIdentityProviderClientID   string  `json:"IdentityProviderClientID"`
	M_sIdentityProviderURL        string  `json:"IdentityProviderURL"`
	M_sIdentityProviderOIDCScope  string  `json:"IdentityProviderOIDCScope"`
	M_bSupportsTLS                bool    `json:"SupportsTLS"`
	M_byOffersLegacyPort          byte    `json:"OffersLegacyPort"`
	M_byOffersHTTPSPort           byte    `json:"OffersHTTPSPort"`

} // end type gtwConfig

func (gtw *gtwConfig) isLegacyPortAvailable () bool {

	return ! gtw.M_bSupportsTLS || gtw.M_byOffersLegacyPort >= 1

} // end isLegacyPortAvailable

func (gtw *gtwConfig) isHTTPSPortAvailable () bool {

	return gtw.M_byOffersHTTPSPort >= 1

} // end isHTTPSPortAvailable

func parseConfigResponse (con *TeradataConnection, abyParcelBody [] byte) (configOutput *config) {

	configOutput = &config {} // ensure non-nil output

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter parseConfigResponse %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave parseConfigResponse %v", con)) } ()
	}

	configOutput.M_uFirstSessionNumber       = consumeInt (&abyParcelBody)
	configOutput.M_uFirstRequestNumber       = consumeInt (&abyParcelBody)
	configOutput.M_uMaxSessionCount          = consumeShort (&abyParcelBody)
	configOutput.M_uIFPCount                 = consumeShort (&abyParcelBody)

	for i := uint16 (0) ; i < configOutput.M_uIFPCount ; i++ {

		uVProcNumber                        := consumeShort (&abyParcelBody)
		uVProcState                         := consumeByte (&abyParcelBody)

		configOutput.M_aVProcs               = append (configOutput.M_aVProcs, vproc { M_uVProcNumber : uVProcNumber, M_uVProcState : uVProcState })

		skipByte (&abyParcelBody) // pad byte
	}

	configOutput.M_uAMPCount                 = consumeShort (&abyParcelBody)

	for i := uint16 (0) ; i < configOutput.M_uAMPCount ; i++ {

		configOutput.M_auAMPNumbers          = append (configOutput.M_auAMPNumbers, consumeShort (&abyParcelBody))
	}

	configOutput.M_uDefaultCharsetCode       = consumeByte (&abyParcelBody)

	skipByte (&abyParcelBody) // pad byte

	configOutput.M_uCharsetCount             = consumeShort (&abyParcelBody)

	for i := uint16 (0) ; i < configOutput.M_uCharsetCount ; i++ {

		uCharsetCode                        := consumeByte (&abyParcelBody)
		uInternationalHash                  := consumeByte (&abyParcelBody)
		sCharsetName                        := strings.TrimSpace (string (consumeBytes (&abyParcelBody, 30)))

		configOutput.M_aCharsets             = append (configOutput.M_aCharsets, charset { M_sCharsetName : sCharsetName, M_uCharsetCode : uCharsetCode, m_uInternationalHash : uInternationalHash })
	}

	configOutput.M_sInDoubtSessionsExist     = string (consumeByte (&abyParcelBody))
	configOutput.m_uExtensionAreaFollows     = consumeByte (&abyParcelBody)

	for len (abyParcelBody) >= 4 {

		uFeatureType      := consumeShort (&abyParcelBody)
		uFeatureByteCount := consumeShort (&abyParcelBody)
		abyFeatureBody    := abyParcelBody [ : uFeatureByteCount] // constrained

		abyParcelBody      = abyParcelBody [uFeatureByteCount : ] // advance to next feature

		switch uFeatureType {

			case 1: // ANSI Transactions

				configOutput.M_sDefaultTransactionSemantics     = string (consumeByte (&abyFeatureBody))

			case 6: // ANSI Date/Time

				configOutput.M_uANSIDateTimeSupport             = consumeByte (&abyFeatureBody)
				configOutput.M_sDefaultDateForm                 = string (consumeByte (&abyFeatureBody))

			case 7: // Database Limits

				configOutput.M_sDefaultRowSize                  = string (consumeByte (&abyFeatureBody))

				skipByte (&abyFeatureBody) // pad byte

				configOutput.M_uMaxMultiTSRSegmentCount         = consumeShort (&abyFeatureBody)
				configOutput.M_uMaxMultiTSRSegmentSize          = consumeInt (&abyFeatureBody)

				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxRowSizeInBytes                = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 8 { configOutput.M_uMaxLOBSizeInBytes                = consumeLong (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxRequestSizeInBytes            = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxResponseSizeInBytes           = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxSPParameterCount              = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxColumnsInGroupBy              = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxColumnsInOrderBy              = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxColumnsInSelect               = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxColumnsInTable                = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxObjectNameLengthInChars       = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxTablesInSelect                = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxUsingFieldCount               = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxUsingDataSizeInBytes          = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxByteLiteralLengthInChars      = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxCharLiteralLengthInChars      = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxDecimalPrecision              = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxColumnDataSizeInBytes         = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxVarcharLengthInChars          = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxCharLengthInChars             = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxGraphicLengthInChars          = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxVargraphicLengthInChars       = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxVarbyteSizeInBytes            = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxByteSizeInBytes               = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxParameterCountInRequest       = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxTimeScale                     = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxTimestampScale                = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxIntervalToSecondScale         = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxDeferredByNameFileNameBytes   = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxAvailableBytesInResponseRow   = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxJsonByteCount                 = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxRequestTextByteCount          = consumeInt (&abyFeatureBody) }
				if len (abyFeatureBody) >= 4 { configOutput.M_uMaxRecordCountInIteratedRequest  = consumeInt (&abyFeatureBody) }

			case 9: // SSO Support

				configOutput.M_uSSOSupported                    = consumeByte (&abyFeatureBody)

			case 10: // SQL Capabilities

				configOutput.M_uAtomicUpsertSupport             = consumeByte (&abyFeatureBody)
				configOutput.M_uDMLArraySupport                 = consumeByte (&abyFeatureBody)
				configOutput.M_uMergeIntoSupport                = consumeByte (&abyFeatureBody)
				configOutput.M_uOUTParamArgMarkerSupport        = consumeByte (&abyFeatureBody)
				configOutput.M_uLargeDecimalAndBigintSupport    = consumeByte (&abyFeatureBody)
				configOutput.M_uAGKRSupport                     = consumeByte (&abyFeatureBody)
				configOutput.M_uQueryBandSupport                = consumeByte (&abyFeatureBody)
				configOutput.M_uFullANSIMergeSupport            = consumeByte (&abyFeatureBody)
				configOutput.M_uLoggingErrorsSupport            = consumeByte (&abyFeatureBody)
				configOutput.M_uColumnCorrelationSupport        = consumeByte (&abyFeatureBody)
				configOutput.M_uTemporalSemanticsSupport        = consumeByte (&abyFeatureBody)
				configOutput.M_uQueryBandProxyUserSupport       = consumeByte (&abyFeatureBody)

				if len (abyFeatureBody) >= 1 { configOutput.M_uUserDefinedIndexSupport          = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uTrustedSQLRequestSupport         = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uMandatoryAccessControlSupport    = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uUserDefinedOperatorSupport       = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uXMLDataTypeAndXQuerySupport      = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uTemporalEnabledLevel             = consumeByte (&abyFeatureBody) }

				if len (abyFeatureBody) >= 1 { skipByte (&abyFeatureBody) } // unused

				if len (abyFeatureBody) >= 1 { configOutput.M_uPartitionLevelLockingSupport     = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uBooleanDataTypeSupport           = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uJSONDataTypeSupport              = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uSecureZonesSupport               = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uUnicodePassThroughSupport        = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uAvroDatasetDataTypeSupport       = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uCSVDatasetDataTypeSupport        = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uTransformGroupForType            = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uODBCScalarFunctions              = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uOTFSupport                       = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uVSDSupport                       = consumeByte (&abyFeatureBody) }

			case 11: // Wire Protocol

				configOutput.M_uMultipartParcelAndLOBSupport    = consumeByte (&abyFeatureBody)
				configOutput.M_uAPHRequestSupport               = consumeByte (&abyFeatureBody)
				configOutput.M_u1MBExtendedResponseSupport      = consumeByte (&abyFeatureBody)

				if len (abyFeatureBody) >= 1 { configOutput.M_uSSRCursorPositioningSupport      = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uUDTSupport                       = consumeByte (&abyFeatureBody) }

				if len (abyFeatureBody) >= 1 { skipByte (&abyFeatureBody) } // unused

				if len (abyFeatureBody) >= 1 { configOutput.M_uAPHResponseSupport               = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uSIPResponseSupport               = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uIncreasedHashBucketsSupport      = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uSPDynamicResultSetsSupport       = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uMSRCursorPositioningSupport      = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uSIPRequestSupport                = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uStructSupport                    = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uPeriodDataTypeSupport            = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uElicitByNameSupport              = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uClientAttributesParcelSupport    = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uFetchRowCountParcelSupport       = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uStatementIndependenceSupport     = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uRNPSupport                       = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uArrayDataTypeSupport             = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uRowDataTypeSupport               = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uRedriveSupport                   = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uControlDataSupport               = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uOverrideGeneratedValuesSupport   = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uNumberDataTypeSupport            = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uEnhancedStatementStatusSupport   = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uTDWMFailFastSupport              = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uUnityClientAttributesSupport     = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uTDWMUnitySupport                 = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uUnityStoredProcedureSupport      = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uSLOBClientToServerSupport        = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uSLOBServerToClientSupport        = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uUnityCDCSupport                  = consumeByte (&abyFeatureBody) }

			case 12: // Load Protocols

				configOutput.M_uLoadIdentityColumnSupport       = consumeByte (&abyFeatureBody)

				if len (abyFeatureBody) >= 1 { configOutput.M_uFastExportNoSpoolSupport         = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uCheckWorkloadSupport             = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uMLoadXProtocolSupport            = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uMonitorPartitionAsyncAbort       = consumeByte (&abyFeatureBody) }
				if len (abyFeatureBody) >= 1 { configOutput.M_uMultipleTransformSupport         = consumeByte (&abyFeatureBody) }

			case 13: // Database Release and Version

				configOutput.M_sPDEVersion                      = strings.TrimSpace (string (consumeBytes (&abyFeatureBody, 30)))
				configOutput.M_sDatabaseVersion                 = strings.TrimSpace (string (consumeBytes (&abyFeatureBody, 32)))
				configOutput.M_uDatabaseVersionNumber           = composeParseAllUint32 (configOutput.M_sDatabaseVersion, /*uDigitsPerNumber*/ 2, /*uNumberCount*/ 3)

			case 14: // Extended Object Names

				configOutput.M_uExtendedObjectNamesOnDisk       = consumeByte (&abyFeatureBody)
				configOutput.M_uExtendedObjectNamesInViews      = consumeByte (&abyFeatureBody)
				configOutput.M_uExtendedObjectNameProcessing    = consumeByte (&abyFeatureBody)
				configOutput.M_uExtendedObjectNamesInParcels    = consumeByte (&abyFeatureBody)

		} // end switch

	} // end for

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("Config Response = %+v", configOutput))
	}

	return

} // end method parseConfigResponse

func parseGtwConfig (con *TeradataConnection, abyParcelHeader [] byte) (gtwConfigOutput *gtwConfig) {

	gtwConfigOutput = &gtwConfig {} // ensure non-nil output

	abyParcelBody := constrainedParcelBody (abyParcelHeader)

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter parseGtwConfig %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave parseGtwConfig %v", con)) } ()
	}

	uSelfDefining := consumeInt (&abyParcelBody)
	if uSelfDefining == 0 { return }

	for len (abyParcelBody) >= 4 {

		// PCLGTWCONFIG feature length includes feature header and feature body
		uFeatureType       := consumeShort (&abyParcelBody)
		uFeatureLength     := consumeShort (&abyParcelBody)
		uFeatureBodyLength := uFeatureLength - 4 // remove 4 bytes for the feature header
		abyFeature         := abyParcelBody [ : uFeatureBodyLength] // constrained
		abyParcelBody       = abyParcelBody [uFeatureBodyLength : ] // advance to next feature

		switch uFeatureType {

			case 1: // Gateway SSO Support
				gtwConfigOutput.M_uSSOSupport = consumeByte (&abyFeature)

			case 2: // TDGSS Version
				gtwConfigOutput.m_abyTDGSSVersion = consumeBytes (&abyFeature, 4)
				gtwConfigOutput.M_sTDGSSVersion   = fmt.Sprintf ("%02d.%02d.%02d.%02d", gtwConfigOutput.m_abyTDGSSVersion [0], gtwConfigOutput.m_abyTDGSSVersion [1], gtwConfigOutput.m_abyTDGSSVersion [2], gtwConfigOutput.m_abyTDGSSVersion [3])

			case 3: // UTF-16 Credentials support
				gtwConfigOutput.M_bUTF16Credentials = true

			case 4: // Gateway Node Id
				gtwConfigOutput.M_uGatewayNodeId = consumeShort (&abyFeature)

			case 5: // Recoverable Network Protocol
				gtwConfigOutput.M_bRecoverableProtocol = true

			case 6: // Control Data
				gtwConfigOutput.M_bControlData = true

			case 7: // Redrive
				gtwConfigOutput.M_bRedrive = true

			case 10: // Security Policy Support
				gtwConfigOutput.M_uSecurityPolicy = consumeByte (&abyFeature)

			case 12: // Negotiation mechanism support
				gtwConfigOutput.M_uNegoMech = consumeByte (&abyFeature)

			case 15: // PCLGTWCONFIGIDP
				gtwConfigOutput.M_sIdentityProviderClientID = consumeVarcharUTF8 (&abyFeature)
				gtwConfigOutput.M_sIdentityProviderURL      = consumeVarcharUTF8 (&abyFeature)

			case 16: // PCLGTWCONFIGPORTS
				gtwConfigOutput.M_bSupportsTLS = true
				gtwConfigOutput.M_byOffersLegacyPort = consumeByte (&abyFeature)
				gtwConfigOutput.M_byOffersHTTPSPort  = consumeByte (&abyFeature)

			case 17: // PCLGTWCONFIGIDPSCOPE
				gtwConfigOutput.M_sIdentityProviderOIDCScope = consumeVarcharUTF8 (&abyFeature)

		} // end switch
	} // end for

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("GtwConfig Response = %+v", gtwConfigOutput))
	}

	return

} // end method parseGtwConfig
