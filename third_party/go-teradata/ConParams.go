// Copyright 2017-2022 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type ConParams struct {

	// uppercase letter M_ prefix for public fields parsed from JSON

	M_sAccount              string   `json:"account"`
	M_sApplicationESN       string   `json:"application_esn"`
	M_sBrowser              string   `json:"browser"`
	M_nBrowserTabTimeout    int32    `json:"browser_tab_timeout,string"`
	M_uBrowserTimeout       uint32   `json:"browser_timeout,string"`
	M_sClientDataSource     string   `json:"client_datasource"`
	M_sClientExtra          string   `json:"client_extra"`
	M_sClientKind           string   `json:"client_kind"`
	M_sClientOsName         string   `json:"client_osname"`
	M_sClientStack          string   `json:"client_stack"`
	M_sClientVmName         string   `json:"client_vmname"`
	M_sCodeAppendFile       string   `json:"code_append_file"`
	M_bColumnName           bool     `json:"column_name,string"`
	M_sControlESN           string   `json:"control_esn"`
	M_uLCCIntervalMs        uint32   `json:"concurrent_interval,string"`
	M_uLCCLimit             uint32   `json:"concurrent_limit,string"`
	M_uConnectFailureTTL    uint32   `json:"connect_failure_ttl,string"`
	M_uConnectFunction      uint16   `json:"connect_function,string"`
	M_uConnectTimeoutMs     uint32   `json:"connect_timeout,string"`
	M_uConnHandle           uint64   `json:"connection_handle,string"`
	M_bCop                  bool     `json:"cop,string"`
	M_bCopLast              bool     `json:"coplast,string"`
	M_sDatabase             string   `json:"database"`
	M_sDatabaseLSN          string   `json:"database_lsn"`
	M_uDbsPort              uint32   `json:"dbs_port,string"`
	M_bEncryptData          bool     `json:"encryptdata,string"`
	M_uErrorQueryCount      uint32   `json:"error_query_count,string"`
	M_uErrorQueryInterval   uint32   `json:"error_query_interval,string"`
	M_sErrorTable1Suffix    string   `json:"error_table_1_suffix"`
	M_sErrorTable2Suffix    string   `json:"error_table_2_suffix"`
	M_sErrorTableDatabase   string   `json:"error_table_database"`
	M_bFakeResultSets       bool     `json:"fake_result_sets,string"`
	M_sFieldQuote           string   `json:"field_quote"`
	M_sFieldSeparator       string   `json:"field_sep"`
	M_uGatewayDeadline      uint32   `json:"gateway_deadline,string"`
	M_bGovern               bool     `json:"govern,string"`
	M_sHostName             string   `json:"host"`
	M_sHTTPProxy            string   `json:"http_proxy"`
	M_sHTTPProxyPassword    string   `json:"http_proxy_password"`
	M_sHTTPProxyUser        string   `json:"http_proxy_user"`
	M_uHTTPSPort            uint32   `json:"https_port,string"`
	M_sHTTPSProxy           string   `json:"https_proxy"`
	M_sHTTPSProxyPassword   string   `json:"https_proxy_password"`
	M_sHTTPSProxyUser       string   `json:"https_proxy_user"`
	M_uHTTPSRetry           uint32   `json:"https_retry,string"`
	M_bImmediate            bool     `json:"immediate,string"`
	M_sJWSAlgorithm         string   `json:"jws_algorithm"`
	M_sJWSCert              string   `json:"jws_cert"`
	M_sJWSPrivateKey        string   `json:"jws_private_key"`
	M_bLOBSupport           bool     `json:"lob_support,string"`
	M_bLocalCatalog         bool     `json:"local_catalog,string"`
	M_uLog                  uint64   `json:"log,string"`
	M_sLogData              string   `json:"logdata"`
	M_sLogMech              string   `json:"logmech"`
	M_uLogonSequenceNumber  uint32   `json:"logon_sequence_number,string"`
	M_uLogonTimeout         uint32   `json:"logon_timeout,string"`
	M_bManageErrorTables    bool     `json:"manage_error_tables,string"`
	M_uMaxMessageBodySize   uint32   `json:"max_message_body,string"`
	M_uOAuthLevel           uint32   `json:"oauth_level,string"`
	M_sOAuthScopes          string   `json:"oauth_scopes"`
	M_uOIDCCacheSize        uint32   `json:"oidc_cache_size,string"`
	M_sOIDCClaim            string   `json:"oidc_claim"`
	M_sOIDCClientID         string   `json:"oidc_clientid"`
	M_sOIDCMetadata         string   `json:"oidc_metadata"`
	M_uOIDCMetadataCache    uint32   `json:"oidc_metadata_cache,string"`
	M_sOIDCPrompt           string   `json:"oidc_prompt"`
	M_sOIDCRedirectPort     string   `json:"oidc_redirect_port"`
	M_bOIDCRefresh          bool     `json:"oidc_refresh,string"`
	M_uOIDCRefreshPercent   uint32   `json:"oidc_refresh_percent,string"`
	M_sOIDCScope            string   `json:"oidc_scope"`
	M_sOIDCSSLMode          string   `json:"oidc_sslmode"`
	M_sOIDCToken            string   `json:"oidc_token"`
	M_sPartition            string   `json:"partition"`
	M_sPassword             string   `json:"password"`
	M_bPOSIXlt              bool     `json:"posixlt,string"`
	M_sProxyBypassHosts     string   `json:"proxy_bypass_hosts"`
	M_uReconnectCount       uint32   `json:"reconnect_count,string"`
	M_uReconnectInterval    uint32   `json:"reconnect_interval,string"`
	M_uRedrive              uint32   `json:"redrive,string"`
	M_uRequestTimeout       uint32   `json:"request_timeout,string"`
	M_bRunStartup           bool     `json:"runstartup,string"`
	M_uSessions             uint32   `json:"sessions,string"`
	M_bSIPSupport           bool     `json:"sip_support,string"`
	M_bSaveSPL              bool     `json:"sp_spl,string"`
	M_sSSLBase64            string   `json:"sslbase64"`
	M_sSSLCAFile            string   `json:"sslca"`
	M_sSSLCAPath            string   `json:"sslcapath"`
	M_sSSLCipher            string   `json:"sslcipher"`
	M_sSSLCRC               string   `json:"sslcrc"`
	M_bSSLCRL               bool     `json:"sslcrl,string"`
	M_sSSLMode              string   `json:"sslmode"`
	M_sSSLNamedGroups       string   `json:"sslnamedgroups"`
	M_bSSLOCSP              bool     `json:"sslocsp,string"`
	M_sSSLProtocol          string   `json:"sslprotocol"`
	M_bTeradataValues       bool     `json:"teradata_values,string"`
	M_bTokenMode            bool     `json:"token_mode,string"`
	M_sTMode                string   `json:"tmode"`
	M_sUserName             string   `json:"user"`
	M_sWsEndpoint           string   `json:"ws_endpoint"`

	// lowercase letter m_ prefix for private fields not in JSON, instead set by ParseConParams

	m_nSSLModeLevel         int
	m_nOIDCSSLModeLevel     int
	m_auTLSCiphers          [] uint16 // nil when sslcipher connection parameter is omitted
	m_auTLSCurveIDs         [] tls.CurveID
	m_uTLSVersion           uint16
	m_urlHTTPProxy          *url.URL
	m_urlHTTPSProxy         *url.URL
	m_anRedirectPortRange   [] int

} // end type ConParams

const (
	LEVEL_DISABLE     = 1
	LEVEL_ALLOW       = 2
	LEVEL_PREFER      = 3
	LEVEL_REQUIRE     = 4
	LEVEL_VERIFY_CA   = 5
	LEVEL_VERIFY_FULL = 6
)

var regexPortRange = regexp.MustCompile (`^([0-9]{1,5})(?:-([0-9]{1,5}))?$`)

func ParseConParams (sConParams string) (params *ConParams, err error) {

	params = &ConParams { // initialize with default values
		M_nBrowserTabTimeout    : 5          ,
		M_uBrowserTimeout       : 180        ,
		M_uConnectTimeoutMs     : 10000      ,
		M_sCodeAppendFile       : "-out"     ,
		M_bCop                  : true       ,
		M_uDbsPort              : 1025       ,
		M_uErrorQueryCount      : 21         ,
		M_uErrorQueryInterval   : 500        ,
		M_sErrorTable1Suffix    : "_ERR_1"   ,
		M_sErrorTable2Suffix    : "_ERR_2"   ,
		M_sFieldQuote           : "\""       ,
		M_sFieldSeparator       : ","        ,
		M_uGatewayDeadline      : 50         , // 50 seconds
		M_bGovern               : true       ,
		M_uHTTPSPort            : 443        ,
		M_uHTTPSRetry           : 2          ,
		M_bImmediate            : true       ,
		M_sJWSAlgorithm         : "RS256"    ,
		M_uLCCIntervalMs        : 1000       ,
		M_uLCCLimit             : 3          ,
		M_bLOBSupport           : true       ,
		M_sLogMech              : "TD2"      ,
		M_bManageErrorTables    : true       ,
		M_uMaxMessageBodySize   : 2097000    ,
		M_uOIDCCacheSize        : 100        ,
		M_sOIDCClaim            : "email"    ,
		M_uOIDCMetadataCache    : 600        , // 600 seconds = 10 minutes
		M_bOIDCRefresh          : true       ,
		M_uOIDCRefreshPercent   : 15         ,
		M_sOIDCToken            : "access_token",
		M_sPartition            : "DBC/SQL"  ,
		M_uRedrive              : 3          ,
		M_bSIPSupport           : true       ,
		M_bSaveSPL              : true       ,
		M_sSSLCRC               : "ALLOW"    ,
		M_bSSLCRL               : true       ,
		M_sSSLMode              : "PREFER"   ,
		M_bSSLOCSP              : true       ,
		M_sSSLProtocol          : "TLSv1.2"  ,
		M_sTMode                : "DEFAULT"  ,
	}

	dec := json.NewDecoder (strings.NewReader (sConParams))
	dec.DisallowUnknownFields ()

	err = dec.Decode (params)
	if err != nil {
		err = formatError (err, "Unable to parse JSON connection parameters")
		return
	}

	params.M_sHostName = strings.TrimSpace (params.M_sHostName)

	params.M_sWsEndpoint = strings.TrimSpace (params.M_sWsEndpoint)
	if params.M_sWsEndpoint != "" {

		var urlEndpoint *url.URL

		urlEndpoint, err = url.Parse (params.M_sWsEndpoint)
		if err != nil {
			err = formatError (err, "ws_endpoint connection parameter value %v is not a valid URI", params.M_sWsEndpoint)
			return
		}

		if urlEndpoint.Scheme == "" {
			err = formatError (nil, "ws_endpoint connection parameter value %v is missing the required scheme https", params.M_sWsEndpoint)
			return
		}

		if urlEndpoint.Scheme != "https" {
			err = formatError (nil, "ws_endpoint connection parameter value %v contains invalid scheme %v instead of the required scheme https", params.M_sWsEndpoint, urlEndpoint.Scheme)
			return
		}

		if urlEndpoint.RawQuery != "" {
			err = formatError (nil, "ws_endpoint connection parameter value %v contains URI query %v which is not permitted", params.M_sWsEndpoint, urlEndpoint.RawQuery)
			return
		}

		if urlEndpoint.Fragment != "" {
			err = formatError (nil, "ws_endpoint connection parameter value %v contains URI fragment %v which is not permitted", params.M_sWsEndpoint, urlEndpoint.Fragment)
			return
		}

		params.M_bTokenMode = true
		if params.M_sHostName == "" { params.M_sHostName = "-none" }

	} // end if M_sWsEndpoint

	params.M_sLogMech = strings.ToUpper (strings.TrimSpace (params.M_sLogMech))
	switch params.M_sLogMech {

		case "BEARER", "BROWSER", "CODE", "CRED", "JWT", "KRB5", "LDAP", "NONE", "ROPC", "SECRET", "TD2", "TDNEGO": // valid logon mechanisms
			// do nothing

		case "":
			err = formatError (nil, "A logon mechanism must be specified for the logmech connection parameter")
			return

		default:
			err = formatError (nil, "Invalid argument %v was specified for the logmech connection parameter", params.M_sLogMech)
			return
	}

	switch params.M_sLogMech {

		case "BEARER", "BROWSER", "CODE", "CRED", "ROPC", "SECRET": // OIDC logon mechanisms
			// do nothing

		default:
			if params.M_bTokenMode {
				err = formatError (nil, "Token mode is not available for logmech %v", params.M_sLogMech)
				return
			}
	}

	if params.M_bTokenMode && params.M_sOAuthScopes != "" {
		err = formatError (nil, "Do not specify oauth_scopes with token mode")
		return
	}

	if params.M_sHostName == "" {
		err = formatError (nil, "A hostname or IP address must be specified for the host connection parameter")
		return
	}

	if params.M_sHostName == "-none" {
		if ! params.M_bTokenMode {
			err = formatError (nil, "Host -none is only allowed in token mode")
			return
		}

		if params.M_sOIDCMetadata == "" {
			err = formatError (nil, "Host -none requires oidc_metadata")
			return
		}

		if params.M_sOIDCClientID == "" {
			err = formatError (nil, "Host -none requires oidc_clientid")
			return
		}
	}

	if params.M_uDbsPort == params.M_uHTTPSPort {
		err = formatError (nil, "Different values must be specified for the dbs_port and https_port connection parameters, but the same value %v was specified", params.M_uDbsPort)
		return
	}

	if params.M_sSSLCipher != "" { // leave m_auTLSCiphers nil when sslcipher connection parameter is omitted

		uID, bFound := g_mapCipherNameToID [params.M_sSSLCipher]

		if bFound {
			params.m_auTLSCiphers = [] uint16 { uID }
		} else {
			err = formatError (nil, "Invalid argument %v was specified for the sslcipher connection parameter", params.M_sSSLCipher)
			return
		}
	} // end if M_sSSLCipher

	if params.M_sSSLNamedGroups != "" { // leave m_auTLSCurveIDs nil when sslnamedgroups connection parameter is omitted

		asNamedGroups := strings.Split (params.M_sSSLNamedGroups, ",")
		for _, sNamedGroup := range asNamedGroups {

			uCurveID, bFound := g_mapNamedGroupToCurveID [sNamedGroup]

			if bFound {
				params.m_auTLSCurveIDs = append (params.m_auTLSCurveIDs, uCurveID)
			} else {
				err = formatError (nil, "Invalid named group %v was specified for the sslnamedgroups connection parameter", sNamedGroup)
				return
			}
		} // end for asNamedGroups
	} // end if M_sSSLNamedGroups

	params.M_sSSLCRC = strings.ToUpper (strings.TrimSpace (params.M_sSSLCRC))
	switch params.M_sSSLCRC {

		case "ALLOW", "PREFER", "REQUIRE": // valid values

		default:
			err = formatError (nil, "Invalid argument %v was specified for the sslcrc connection parameter", params.M_sSSLCRC)
			return

	} // end switch M_sSSLCRC

	if ! params.M_bSSLCRL && ! params.M_bSSLOCSP {
		err = formatError (nil, "sslcrl and sslocsp connection parameters cannot both be false")
		return
	}

	params.M_sSSLMode = strings.ToUpper (strings.TrimSpace (params.M_sSSLMode))
	switch params.M_sSSLMode {

		case "DISABLE"     : params.m_nSSLModeLevel = LEVEL_DISABLE
		case "ALLOW"       : params.m_nSSLModeLevel = LEVEL_ALLOW
		case "PREFER"      : params.m_nSSLModeLevel = LEVEL_PREFER
		case "REQUIRE"     : params.m_nSSLModeLevel = LEVEL_REQUIRE
		case "VERIFY-CA"   : params.m_nSSLModeLevel = LEVEL_VERIFY_CA
		case "VERIFY-FULL" : params.m_nSSLModeLevel = LEVEL_VERIFY_FULL

		default:
			err = formatError (nil, "Invalid argument %v was specified for the sslmode connection parameter", params.M_sSSLMode)
			return

	} // end switch M_sSSLMode

	params.M_sOIDCSSLMode = strings.ToUpper (strings.TrimSpace (params.M_sOIDCSSLMode))
	switch params.M_sOIDCSSLMode {

		case ""            : params.m_nOIDCSSLModeLevel = params.m_nSSLModeLevel // default to the same value as sslmode
		case "ALLOW"       : params.m_nOIDCSSLModeLevel = LEVEL_ALLOW
		case "VERIFY-CA"   : params.m_nOIDCSSLModeLevel = LEVEL_VERIFY_CA
		case "VERIFY-FULL" : params.m_nOIDCSSLModeLevel = LEVEL_VERIFY_FULL

		default:
			err = formatError (nil, "Invalid argument %v was specified for the oidc_sslmode connection parameter", params.M_sOIDCSSLMode)
			return

	} // end switch M_sOIDCSSLMode

	var bFound bool
	params.m_uTLSVersion, bFound = g_mapTLSVersionNameToID [params.M_sSSLProtocol]

	if ! bFound {
		err = formatError (nil, "Invalid argument %v was specified for the sslprotocol connection parameter", params.M_sSSLProtocol)
		return
	}

	switch params.M_sTMode {

		case "ANSI", "TERA", "DEFAULT": // case-sensitive

		default:
			err = formatError (nil, "Invalid argument %v was specified for the tmode connection parameter", params.M_sTMode)
			return
	}

	if ! validFieldDelimiter (params.M_sFieldQuote) {
		err = formatError (nil, "Invalid argument %v was specified for the field_quote connection parameter", params.M_sFieldQuote)
		return

	} // end switch M_sTMode

	if ! validFieldDelimiter (params.M_sFieldSeparator) {
		err = formatError (nil, "Invalid argument %v was specified for the field_sep connection parameter", params.M_sFieldSeparator)
		return
	}

	if params.M_sFieldQuote == params.M_sFieldSeparator {
		err = formatError (nil, "Different values must be specified for the field_quote and field_sep connection parameters, but the same value %v was specified", params.M_sFieldQuote)
		return
	}

	params.M_sHTTPProxy        = strings.TrimSpace (params.M_sHTTPProxy)
	params.M_sHTTPSProxy       = strings.TrimSpace (params.M_sHTTPSProxy)
	params.M_sProxyBypassHosts = strings.TrimSpace (params.M_sProxyBypassHosts)

	if params.M_sProxyBypassHosts != "" && params.M_sHTTPProxy == "" && params.M_sHTTPSProxy == "" {
		err = formatError (nil, "proxy_bypass_hosts requires http_proxy or https_proxy")
		return
	}

	if params.M_sHTTPProxyUser != "" && params.M_sHTTPProxy == "" {
		err = formatError (nil, "http_proxy_user requires http_proxy")
		return
	}

	if params.M_sHTTPProxyPassword != "" && params.M_sHTTPProxy == "" {
		err = formatError (nil, "http_proxy_password requires http_proxy")
		return
	}

	if params.M_sHTTPProxyUser != "" && params.M_sHTTPProxyPassword == "" {
		err = formatError (nil, "http_proxy_user requires http_proxy_password")
		return
	}

	if params.M_sHTTPProxyPassword != "" && params.M_sHTTPProxyUser == "" {
		err = formatError (nil, "http_proxy_password requires http_proxy_user")
		return
	}

	if params.M_sHTTPSProxyUser != "" && params.M_sHTTPSProxy == "" {
		err = formatError (nil, "https_proxy_user requires https_proxy")
		return
	}

	if params.M_sHTTPSProxyPassword != "" && params.M_sHTTPSProxy == "" {
		err = formatError (nil, "https_proxy_password requires https_proxy")
		return
	}

	if params.M_sHTTPSProxyUser != "" && params.M_sHTTPSProxyPassword == "" {
		err = formatError (nil, "https_proxy_user requires https_proxy_password")
		return
	}

	if params.M_sHTTPSProxyPassword != "" && params.M_sHTTPSProxyUser == "" {
		err = formatError (nil, "https_proxy_password requires https_proxy_user")
		return
	}

	params.m_urlHTTPProxy, err = parseConParamURL ("http_proxy", params.M_sHTTPProxy)
	if err != nil { return }

	params.m_urlHTTPSProxy, err = parseConParamURL ("https_proxy", params.M_sHTTPSProxy)
	if err != nil { return }

	if params.M_sProxyBypassHosts != "" {
		_, err = regexp.Compile (convertProxyBypassHostsToRegex (params.M_sProxyBypassHosts))
		if err != nil {
			err = formatError (err, "proxy_bypass_hosts connection parameter value %v contains invalid pattern", params.M_sProxyBypassHosts)
			return
		}
	}

	params.m_anRedirectPortRange = [] int {0, 0} // default to port 0 only (ephemeral port)

	if params.M_sOIDCRedirectPort != "" {

		asPortRange := regexPortRange.FindStringSubmatch (params.M_sOIDCRedirectPort)
		if asPortRange == nil {
			err = formatError (nil, "oidc_redirect_port connection parameter value %v is not a valid port or port range", params.M_sOIDCRedirectPort)
			return
		}

		s1 := asPortRange [1] // skip index 0 which is the entire matched string
		s2 := coalesce (asPortRange [2], s1)

		asPorts := [] string {s1, s2}
		for i, sPort := range asPorts {
			nPort, errAtoi := strconv.Atoi (sPort)
			if errAtoi != nil {
				err = formatError (errAtoi, "oidc_redirect_port connection parameter value %v contains invalid port number %v", params.M_sOIDCRedirectPort, sPort)
				return
			}

			params.m_anRedirectPortRange [i] = nPort
		}

		if params.m_anRedirectPortRange [0] == 0 && params.m_anRedirectPortRange [1] != 0 { // if the first is ephemeral but the second is not
			err = formatError (nil, "oidc_redirect_port connection parameter value %v contains invalid port range", params.M_sOIDCRedirectPort)
			return
		}

		if params.m_anRedirectPortRange [0] > params.m_anRedirectPortRange [1] { // if the first is bigger than the second
			err = formatError (nil, "oidc_redirect_port connection parameter value %v contains invalid port range", params.M_sOIDCRedirectPort)
			return
		}

		nRange := params.m_anRedirectPortRange [1] - params.m_anRedirectPortRange [0]
		if nRange > 100 {
			err = formatError (nil, "oidc_redirect_port connection parameter value %v contains invalid port range", params.M_sOIDCRedirectPort)
			return
		}
	} // end if M_sOIDCRedirectPort

	return

} // end func ParseConParams

func parseConParamURL (sName string, sURL string) (urlOutput *url.URL, err error) {

	if sURL == "" { return }

	urlOutput, err = url.Parse (sURL)
	if err != nil {
		err = formatError (err, "%v connection parameter value %v is not a valid URI", sName, sURL)
		return
	}

	if urlOutput.Scheme == "" {
		err = formatError (nil, "%v connection parameter value %v is missing the required scheme http", sName, sURL)
		return
	}

	if urlOutput.Scheme != "http" {
		err = formatError (nil, "%v connection parameter value %v contains invalid scheme %v instead of the required scheme http", sName, sURL, urlOutput.Scheme)
		return
	}

	if urlOutput.User != nil && urlOutput.User.Username () != "" {
		err = formatError (nil, "%v connection parameter value %v contains user information %v which is not permitted", sName, sURL, urlOutput.User)
		return
	}

	if urlOutput.Path != "" {
		err = formatError (nil, "%v connection parameter value %v contains URI path %v which is not permitted", sName, sURL, urlOutput.Path)
		return
	}

	if urlOutput.RawQuery != "" {
		err = formatError (nil, "%v connection parameter value %v contains URI query %v which is not permitted", sName, sURL, urlOutput.RawQuery)
		return
	}

	if urlOutput.Fragment != "" {
		err = formatError (nil, "%v connection parameter value %v contains URI fragment %v which is not permitted", sName, sURL, urlOutput.Fragment)
		return
	}

	if urlOutput.Port () == "" {
		err = formatError (nil, "%v connection parameter value %v is missing the required port number", sName, sURL)
		return
	}

	return

} // end func parseConParamURL

func (params *ConParams) hasTraceLog  () bool { return (params.M_uLog &  1) != 0 }
func (params *ConParams) hasDebugLog  () bool { return (params.M_uLog &  2) != 0 }
func (params *ConParams) hasDumpLog   () bool { return (params.M_uLog &  4) != 0 }
func (params *ConParams) hasTimingLog () bool { return (params.M_uLog &  8) != 0 }

func (params *ConParams) getDelegatedConParams () map [string] string {

	return map [string] string {
		"account"               : params.M_sAccount,
		"browser_tab_timeout"   : fmt.Sprintf ("%v", params.M_nBrowserTabTimeout),
		"browser_timeout"       : utoa (params.M_uBrowserTimeout),
		"browser"               : params.M_sBrowser,
		"client_datasource"     : params.M_sClientDataSource,
		"client_extra"          : params.M_sClientExtra,
		"client_kind"           : params.M_sClientKind,
		"client_osname"         : params.M_sClientOsName,
		"client_stack"          : params.M_sClientStack,
		"client_vmname"         : params.M_sClientVmName,
		"code_append_file"      : params.M_sCodeAppendFile,
		"concurrent_interval"   : utoa (params.M_uLCCIntervalMs),
		"concurrent_limit"      : utoa (params.M_uLCCLimit),
		"connect_failure_ttl"   : utoa (params.M_uConnectFailureTTL),
		"connect_timeout"       : utoa (params.M_uConnectTimeoutMs),
		"cop"                   : btoa (params.M_bCop),
		"coplast"               : btoa (params.M_bCopLast),
		"dbs_port"              : utoa (params.M_uDbsPort),
		"encryptdata"           : btoa (params.M_bEncryptData),
		"gateway_deadline"      : utoa (params.M_uGatewayDeadline),
		"govern"                : btoa (params.M_bGovern),
		"host"                  : params.M_sHostName,
		"http_proxy"            : params.M_sHTTPProxy,
		"http_proxy_password"   : params.M_sHTTPProxyPassword,
		"http_proxy_user"       : params.M_sHTTPProxyUser,
		"https_port"            : utoa (params.M_uHTTPSPort),
		"https_proxy"           : params.M_sHTTPSProxy,
		"https_proxy_password"  : params.M_sHTTPSProxyPassword,
		"https_proxy_user"      : params.M_sHTTPSProxyUser,
		"https_retry"           : utoa (params.M_uHTTPSRetry),
		"jws_algorithm"         : params.M_sJWSAlgorithm,
		"jws_cert"              : params.M_sJWSCert,
		"jws_private_key"       : params.M_sJWSPrivateKey,
		"log"                   : fmt.Sprintf ("%v", params.M_uLog),
		"logdata"               : params.M_sLogData,
		"logmech"               : params.M_sLogMech,
		"logon_timeout"         : utoa (params.M_uLogonTimeout),
		"max_message_body"      : utoa (params.M_uMaxMessageBodySize),
		"oauth_level"           : utoa (params.M_uOAuthLevel),
		"oauth_scopes"          : params.M_sOAuthScopes,
		"oidc_cache_size"       : utoa (params.M_uOIDCCacheSize),
		"oidc_claim"            : params.M_sOIDCClaim,
		"oidc_clientid"         : params.M_sOIDCClientID,
		"oidc_metadata_cache"   : utoa (params.M_uOIDCMetadataCache),
		"oidc_metadata"         : params.M_sOIDCMetadata,
		"oidc_prompt"           : params.M_sOIDCPrompt,
		"oidc_redirect_port"    : params.M_sOIDCRedirectPort,
		"oidc_refresh"          : btoa (params.M_bOIDCRefresh),
		"oidc_refresh_percent"  : utoa (params.M_uOIDCRefreshPercent),
		"oidc_scope"            : params.M_sOIDCScope,
		"oidc_sslmode"          : params.M_sOIDCSSLMode,
		"oidc_token"            : params.M_sOIDCToken,
		"password"              : params.M_sPassword,
		"proxy_bypass_hosts"    : params.M_sProxyBypassHosts,
		"reconnect_count"       : utoa (params.M_uReconnectCount),
		"reconnect_interval"    : utoa (params.M_uReconnectInterval),
		"redrive"               : utoa (params.M_uRedrive),
		"sslbase64"             : params.M_sSSLBase64,
		"sslca"                 : params.M_sSSLCAFile,
		"sslcapath"             : params.M_sSSLCAPath,
		"sslcipher"             : params.M_sSSLCipher,
		"sslcrc"                : params.M_sSSLCRC,
		"sslcrl"                : btoa (params.M_bSSLCRL),
		"sslmode"               : params.M_sSSLMode,
		"sslocsp"               : btoa (params.M_bSSLOCSP),
		"sslprotocol"           : params.M_sSSLProtocol,
		"teradata_values"       : btoa (params.M_bTeradataValues),
		"user"                  : params.M_sUserName,
	}
} // end method getDelegatedConParams

func validFieldDelimiter (s string) bool {

	return s != "\r" && s != "\n" && utf8.RuneCountInString (s) == 1 && utf8.ValidRune ([] rune (s) [0]) && [] rune (s) [0] != utf8.RuneError

} // end validFieldDelimiter
