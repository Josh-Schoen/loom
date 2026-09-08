// Copyright 2017 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"encoding/binary"
)

func MessageTitle (abyMessage [] byte, bReverse bool) string {

	if len (abyMessage) < 52 {
		return ""
	}

	uMessageClass := abyMessage [1] & 0x7F

	if bReverse {
		if uMessageClass == 1 {
			uMessageClass = 2
		} else if uMessageClass == 2 {
			uMessageClass = 1
		}
	}

	return MessageKind (abyMessage [2]) + " " + MessageClass (uMessageClass)

} // end func MessageTitle

func MessageBodyLength (abyHeader [] byte) int {
	return int (binary.BigEndian.Uint16 (abyHeader [3 : ])) << 16 | int (binary.BigEndian.Uint16 (abyHeader [8 : ]))
}

func MessageKind (u uint8) string {
	return nameOf (MESSAGE_KIND, int (u))
}

func MessageClass (u uint8) string {
	return nameOf (MESSAGE_CLASS, int (u))
}

func parcelFlavor (u uint16) string {
	return nameOf (FLAVOR, int (u))
}

func activityType (u uint16) string {
	return nameOf (ACTIVITY, int (u))
}

func layoutType (u uint16) string {
	return nameOf (LAYOUT_TYPE, int (u))
}

func contentType (u uint16) string {
	return nameOf (CONTENT_TYPE, int (u))
}

func nameOf (as [] string, i int) string {

	if i >= len (as) || as [i] == "" {
		return "unknown"
	}

	return as [i]

} // end nameOf

const MESSAGE_KIND_ASSIGN          byte =  1
const MESSAGE_KIND_REASSIGN        byte =  2
const MESSAGE_KIND_CONNECT         byte =  3
const MESSAGE_KIND_RECONNECT       byte =  4
const MESSAGE_KIND_START           byte =  5
const MESSAGE_KIND_CONTINUE        byte =  6
const MESSAGE_KIND_ABORT           byte =  7
const MESSAGE_KIND_LOGOFF          byte =  8
const MESSAGE_KIND_CONFIG          byte = 10
const MESSAGE_KIND_AUTHMETHODS     byte = 11
const MESSAGE_KIND_SSO             byte = 12
const MESSAGE_KIND_ELICIT          byte = 13
const MESSAGE_KIND_ELICITSTARTRESP byte = 14
const MESSAGE_KIND_MESSAGERECEIVED byte = 15

var MESSAGE_KIND [] string = [] string {

	"", // 0
	"Assign", // 1
	"Reassign", // 2
	"Connect", // 3
	"Reconnect", // 4
	"Start", // 5
	"Continue", // 6
	"Abort", // 7
	"Logoff", // 8
	"Test", // 9
	"Config", // 10
	"AuthMethods", // 11
	"SSO", // 12
	"Elicit", // 13
	"ElicitStartResp", // 14
	"MessageReceived", // 15

} // end MESSAGE_KIND

const MESSAGE_CLASS_REQUEST  byte = 1
const MESSAGE_CLASS_RESPONSE byte = 2

var MESSAGE_CLASS [] string = [] string {

	"", // 0
	"Request", // 1
	"Response", // 2

} // end MESSAGE_CLASS

const PCLCLIENTCONFIGTTUVERSION          = 1
const PCLCLIENTCONFIGTDGSS               = 2
const PCLCLIENTCONFIGRECOVERABLEPROTOCOL = 3
const PCLCLIENTCONFIGCONTROLDATA         = 4
const PCLCLIENTCONFIGREDRIVE             = 5
const PCLCLIENTCONFIGSECURITYPOLICY      = 8
const PCLCLIENTCONFIGESS                 = 9
const PCLCLIENTCONFIGNEGOMECH            = 11
const PCLCLIENTCONFIGIDP                 = 14
const PCLCLIENTCONFIGTLS                 = 15

const PCLREQUEST                      uint16 =   1
const PCLRUNSTARTUP                   uint16 =   2
const PCLDATA                         uint16 =   3
const PCLRESP                         uint16 =   4
const PCLABORT                        uint16 =   6
const PCLCANCEL                       uint16 =   7
const PCLSUCCESS                      uint16 =   8
const PCLFAILURE                      uint16 =   9
const PCLRECORD                       uint16 =  10
const PCLENDSTATEMENT                 uint16 =  11
const PCLENDREQUEST                   uint16 =  12
const PCLPROMPT                       uint16 =  29
const PCLLOGON                        uint16 =  36
const PCLLOGOFF                       uint16 =  37
const PCLUCABORT                      uint16 =  40
const PCLCONFIGRESP                   uint16 =  43
const PCLERROR                        uint16 =  49
const PCLINDICDATA                    uint16 =  68
const PCLINDICREQ                     uint16 =  69
const PCLDATAINFO                     uint16 =  71
const PCLOPTIONS                      uint16 =  85
const PCLPREPINFO                     uint16 =  86
const PCLCONNECT                      uint16 =  88
const PCLLSN                          uint16 =  89
const PCLASSIGN                       uint16 = 100
const PCLASSIGNRSP                    uint16 = 101
const PCLSESSOPT                      uint16 = 114
const PCLPREPINFOX                    uint16 = 125
const PCLMULTITSR                     uint16 = 128
const PCLSPOPTIONS                    uint16 = 129
const PCLSSOREQ                       uint16 = 132
const PCLSSORESP                      uint16 = 134
const PCLMULTIPARTDATA                uint16 = 140
const PCLENDMULTIPARTDATA             uint16 = 141
const PCLMULTIPARTINDICDATA           uint16 = 142
const PCLENDMULTIPARTINDICDATA        uint16 = 143
const PCLMULTIPARTRECORD              uint16 = 144
const PCLENDMULTIPARTRECORD           uint16 = 145
const PCLDATAINFOX                    uint16 = 146
const PCLELICITDATA                   uint16 = 150
const PCLELICITFILE                   uint16 = 151
const PCLELICITDATARECEIVED           uint16 = 152
const PCLBIGRESP                      uint16 = 153
const PCLBIGKEEPRESP                  uint16 = 154
const PCLGTWCONFIG                    uint16 = 165
const PCLCLIENTCONFIG                 uint16 = 166
const PCLAUTHMECH                     uint16 = 167
const PCLSTATEMENTINFO                uint16 = 169
const PCLSTATEMENTINFOEND             uint16 = 170
const PCLRESULTSET                    uint16 = 172
const PCLCLIENTATTRIBUTES             uint16 = 189
const PCLSTMTERROR                    uint16 = 192
const PCLSESSIONSTATUSRESP            uint16 = 195
const PCLCONTINUECONTEXT              uint16 = 196
const PCLCONTROLDATASTART             uint16 = 197
const PCLCONTROLDATAEND               uint16 = 198
const PCLRECOVERABLEPROTOCOL          uint16 = 200
const PCLREDRIVE                      uint16 = 201
const PCLACKREQUESTED                 uint16 = 202
const PCLREDRIVESUPPORTED             uint16 = 204
const PCLSTMTSTATUS                   uint16 = 205
const PCLSECURITYPOLICY               uint16 = 206
const PCLRECOVERABLENPSUPPORTED       uint16 = 214
const PCLOAUTHTOKENS                  uint16 = 228

var FLAVOR [] string = [] string {

	"", // 0
	"PCLREQUEST", // 1
	"PCLRUNSTARTUP", // 2
	"PCLDATA", // 3
	"PCLRESP", // 4
	"PCLKEEPRESP", // 5
	"PCLABORT", // 6
	"PCLCANCEL", // 7
	"PCLSUCCESS", // 8
	"PCLFAILURE", // 9
	"PCLRECORD", // 10
	"PCLENDSTATEMENT", // 11
	"PCLENDREQUEST", // 12
	"PCLFMREQ", // 13
	"PCLFMRUNSTARTUP", // 14
	"PCLVALUE", // 15
	"PCLNULLVALUE", // 16
	"PCLOK", // 17
	"PCLFIELD", // 18
	"PCLNULLFIELD", // 19
	"PCLTITLESTART", // 20
	"PCLTITLEEND", // 21
	"PCLFORMATSTART", // 22
	"PCLFORMATEND", // 23
	"PCLSIZESTART", // 24
	"PCLSIZEEND", // 25
	"PCLSIZE", // 26
	"PCLRECSTART", // 27
	"PCLRECEND", // 28
	"PCLPROMPT", // 29
	"PCLENDPROMPT", // 30
	"PCLREWIND", // 31
	"PCLNOP", // 32
	"PCLWITH", // 33
	"PCLPOSITION", // 34
	"PCLENDWITH", // 35
	"PCLLOGON", // 36
	"PCLLOGOFF", // 37
	"PCLRUN", // 38
	"PCLRUNRESP", // 39
	"PCLUCABORT", // 40
	"PCLHOSTSTART", // 41
	"PCLCONFIG", // 42
	"PCLCONFIGRESP", // 43
	"PCLSTATUS", // 44
	"PCLIFPSWITCH", // 45
	"PCLPOSSTART", // 46
	"PCLPOSEND", // 47
	"PCLBULKRESP", // 48
	"PCLERROR", // 49
	"PCLDATE", // 50
	"PCLROW", // 51
	"PCLHUTCREDBS", // 52
	"PCLHUTDBLK", // 53
	"PCLHUTDELTBL", // 54
	"PCLHUTINSROW", // 55
	"PCLHUTRBLK", // 56
	"PCLHUTSNDBLK", // 57
	"PCLENDACCLOG", // 58
	"PCLHUTRELDBCLK", // 59
	"PCLHUTNOP", // 60
	"PCLHUTBLD", // 61
	"PCLHUTBLDRSP", // 62
	"PCLHUTGETDDT", // 63
	"PCLHUTGETDDTRSP", // 64
	"PCLHUTIDx", // 65
	"PCLHUTIDxRsp", // 66
	"PCLFIELDSTATUS", // 67
	"PCLINDICDATA", // 68
	"PCLINDICREQ", // 69
	"PCLINDSTATREQ", // 70
	"PCLDATAINFO", // 71
	"PCLIVRUNSTARTUP", // 72
	"PCLHUTRECVY", // 73
	"PCLHUTRECVYRSP", // 74
	"PCLHUTAEN", // 75
	"PCLHUTAENRSP", // 76
	"PCLHUTEVT", // 77
	"PCLHUTUEV", // 78
	"PCLHUTMED", // 79
	"PCLCLEANUP", // 80
	"PCLHUTSTBL", // 81
	"PCLHUTSTBLRSP", // 82
	"PCLHUTBKDN", // 83
	"PCLHUTBKDNRSP", // 84
	"PCLOPTIONS", // 85
	"PCLPREPINFO", // 86
	"PCLIMPLOGON", // 87
	"PCLCONNECT", // 88
	"PCLLSN", // 89
	"PCLHUTCKDMPDONE", // 90
	"", // 91
	"", // 92
	"", // 93
	"", // 94
	"", // 95
	"", // 96
	"", // 97
	"", // 98
	"", // 99
	"PCLASSIGN", // 100
	"PCLASSIGNRSP", // 101
	"PCLMATCHTAG", // 102
	"PCLERECDATA", // 103
	"PCLEINDDATA", // 104
	"PCLERRORCNT", // 105
	"PCLSESSINFO", // 106
	"PCLSESSINFORESP", // 107
	"PCLHUTM3FBPROC", // 108
	"PCLHUTP90FBPROC", // 109
	"PCLHUTM3FBSTID", // 110
	"PCLHUTP90FBSTID", // 111
	"PCLHUTM3CLUSTER", // 112
	"PCLHUTP90CLUSTER", // 113
	"PCLSESSOPT", // 114
	"PCLVOTEREQUEST", // 115
	"PCLVOTETERM", // 116
	"PCLCMMT2PC", // 117
	"PCLABRT2PC", // 118
	"PCLFORGET", // 119
	"PCLCURSORHOST", // 120
	"PCLCURSORDBC", // 121
	"PCLFLAGGER", // 122
	"", // 123
	"", // 124
	"PCLPREPINFOX", // 125
	"PCLSESSINFOX", // 126
	"PCLSESSINFORESPX", // 127
	"PCLMULTITSR", // 128
	"PCLSPOPTIONS", // 129
	"PCLSSOAUTHREQ", // 130
	"PCLSSOAUTHRESP", // 131
	"PCLSSOREQ", // 132
	"PCLSSODOMAIN", // 133
	"PCLSSORESP", // 134
	"PCLSSOAUTHINFO", // 135
	"PCLUSERNAMEREQ", // 136
	"PCLUSERNAMERESP", // 137
	"PCLXSESSINFOREQ", // 138
	"PCLXSESSINFORESP", // 139
	"PCLMULTIPARTDATA", // 140
	"PCLENDMULTIPARTDATA", // 141
	"PCLMULTIPARTINDICDATA", // 142
	"PCLENDMULTIPARTINDICDATA", // 143
	"PCLMULTIPARTRECORD", // 144
	"PCLENDMULTIPARTRECORD", // 145
	"PCLDATAINFOX", // 146
	"PCLMUTIPARTRUNSTARTUP", // 147
	"PCLMULTIPARTREQ", // 148
	"PCLELICITDATAMAILBOX", // 149
	"PCLELICITDATA", // 150
	"PCLELICITFILE", // 151
	"PCLELICITDATARECEIVED", // 152
	"PCLBIGRESP", // 153
	"PCLBIGKEEPRESP", // 154
	"PCLHUTLOB", // 155
	"PCLHUTLOBRSP", // 156
	"PCLSETPOSITION", // 157
	"PCLROWPOSITION", // 158
	"PCLOFFSETPOSITION", // 159
	"PCLEXTLOGON", // 160
	"PCLDIRROLE", // 161
	"PCLDIRPROFILE", // 162
	"PCLAUTHINFO", // 163 (CLI parcel.h says PCLRESULTSUMMARY)
	"PCLERRORINFO", // 164
	"PCLGTWCONFIG", // 165
	"PCLCLIENTCONFIG", // 166
	"PCLAUTHMECH", // 167
	"PCLINTSP", // 168
	"PCLSTATEMENTINFO", // 169
	"PCLSTATEMENTINFOEND", // 170
	"PCLRESULTSUMMARY", // 171
	"PCLRESULTSET", // 172
	"PCLWALFLUSH", // 173
	"PCLWAITFLUSH", // 174
	"PCLRESULTSETCURPOS", // 175
	"PCLELICTDATABYNAME", // 176
	"PCLWALFLUSH_RRR_LAST", // 177
	"PCLWALFLUSH_RRR", // 178
	"PCLRESULTSETSELECTION", // 179
	"PCLLOGONFAILRSN", // 180
	"", // 181
	"", // 182
	"", // 183
	"", // 184
	"", // 185
	"PCLAUTHINFOEON", // 186
	"", // 187
	"", // 188
	"PCLCLIENTATTRIBUTES", // 189
	"PCLFETCHROWCOUNT", // 190
	"PCLDATAINFOSP", // 191
	"PCLSTMTERROR", // 192
	"PCLHOSTSUPPORT", // 193
	"PCLSESSIONSTATUS", // 194
	"PCLSESSIONSTATUSRESP", // 195
	"PCLCONTINUECONTEXT", // 196
	"PCLCONTROLDATASTART", // 197
	"PCLCONTROLDATAEND", // 198
	"PCLCORRELATIONTAG", // 199
	"PCLRECOVERABLEPROTOCOL", // 200
	"PCLREDRIVE", // 201
	"PCLACKREQUESTED", // 202
	"PCLOVERRIDEDBGENVALUES", // 203
	"PCLREDRIVESUPPORTED", // 204
	"PCLSTMTSTATUS", // 205
	"PCLSECURITYPOLICY", // 206
	"PCLPROXYSECURITYPOLICY", // 207
	"PCLSECURITYUSED", // 208
	"PCLMESSAGEAUDITINFO", // 209
	"PCLGTWHOSTGROUP", // 210
	"PCLPROXYCONFIG", // 211
	"PCLSECUREATTRIBUTE", // 212
	"PCLCONTROLDATASUPPORTEDBYDBS", // 213
	"PCLRECOVERABLENPSUPPORTED", // 214
	"PCLINLINELOBRESPONSE", // 215

} // end FLAVOR

var ACTIVITY [] string = [] string {

	"", // 0
	"Select", // 1
	"Insert", // 2
	"Update", // 3
	"Update...Retrieving", // 4
	"Delete", // 5
	"Create Table", // 6
	"Modify Table", // 7
	"Create View", // 8
	"Create Macro", // 9
	"Drop Table", // 10
	"Drop View", // 11
	"Drop Macro", // 12
	"Drop Index", // 13
	"Rename Table", // 14
	"Rename View", // 15
	"Rename Macro", // 16
	"Create Index", // 17
	"Create Database", // 18
	"Create User", // 19
	"Grant", // 20
	"Revoke", // 21
	"Give", // 22
	"Drop Database", // 23
	"Modify Database", // 24
	"Database", // 25
	"BT", // 26
	"ET", // 27
	"Abort", // 28
	"Null", // 29
	"Execute", // 30
	"Comment Set", // 31
	"Comment Returning", // 32
	"Echo", // 33
	"Replace View", // 34
	"Replace Macro", // 35
	"Checkpoint", // 36
	"Delete Journal", // 37
	"RollBack", // 38
	"Release Lock", // 39
	"HUT Config", // 40
	"Verify Checkpoint", // 41
	"Dump Journal", // 42
	"Dump", // 43
	"Restore", // 44
	"RollForward", // 45
	"Delete Database", // 46
	"Clear Dump", // 47
	"Save Dump", // 48
	"Show", // 49
	"Help", // 50
	"Begin Loading", // 51
	"Checkpoint Loading", // 52
	"End Loading", // 53
	"Lin", // 54
	"Grant Logon", // 55
	"Revoke Logon", // 56
	"Begin Acc Log", // 57
	"End Acc Log", // 58
	"Collect Statistics", // 59
	"Drop Statistics", // 60
	"Set Session/Set Time Zone", // 61
	"Begin MLoad", // 62
	"MLoad", // 63
	"Exec MLoad", // 64
	"End MLoad", // 65
	"Release MLoad", // 66
	"MLoad Delete", // 67
	"MLoad Insert", // 68
	"MLoad Update", // 69
	"Begin Delete MLoad", // 70
	"Data Status", // 71
	"", // 72
	"", // 73
	"Begin FastExport", // 74
	"End FastExport", // 75
	"2PC Vote Request", // 76
	"2PC Vote and Terminate Request", // 77
	"2PC Commit", // 78
	"2PC Abort", // 79
	"2PC Forget", // 80
	"", // 81
	"", // 82
	"Set Session Rate", // 83
	"Monitor Session", // 84
	"Identify", // 85
	"Abort Session", // 86
	"Set Resource Rate", // 87
	"", // 88
	"", // 89
	"Commit Work", // 90
	"Monitor Virtual Config", // 91
	"Monitor Physical Config", // 92
	"Monitor Virtual Summary", // 93
	"Monitor Physical Summary", // 94
	"Monitor Virtual Resource", // 95
	"Monitor Physical Resource", // 96
	"Create Trigger", // 97
	"Drop Trigger", // 98
	"Rename Trigger", // 99
	"Replace Trigger", // 100
	"Alter Trigger", // 101
	"", // 102
	"Drop Procedure", // 103
	"Create Procedure", // 104
	"Call", // 105
	"Rename Procedure", // 106
	"Replace Procedure", // 107
	"", // 108
	"Locking Logger", // 109
	"Monitor SQL", // 110
	"Monitor Version", // 111
	"Begin DBQL", // 112
	"End DBQL", // 113
	"Create Role", // 114
	"Drop Role", // 115
	"Grant Role", // 116
	"Revoke Role", // 117
	"Create Profile", // 118
	"Modify Profile", // 119
	"Drop Profile", // 120
	"Set Role", // 121
	"Create Function", // 122
	"Replace Function", // 123
	"Drop Function", // 124
	"Alter Function", // 125
	"Rename Function", // 126
	"Merge Mixed", // 127
	"Merge Update", // 128
	"Merge Insert", // 129
	"Alter Procedure", // 130
	"TDWM Enable", // 131
	"TDWM Statistics", // 132
	"TDWM PerfGroups", // 133
	"Create Type", // 134
	"Drop Type", // 135
	"Alter Type", // 136
	"Replace Type", // 137
	"Create Method", // 138
	"Alter Method", // 139
	"Replace Method", // 140
	"Create Cast", // 141
	"Replace Cast", // 142
	"Drop Cast", // 143
	"Create Ordering", // 144
	"Replace Ordering", // 145
	"Drop Ordering", // 146
	"Create Transform", // 147
	"Replace Transform", // 148
	"Drop Transform", // 149
	"Create Authorization", // 150
	"Drop Authorization", // 151
	"Create Replication Group", // 152
	"Alter Replication Group", // 153
	"Drop Replication Group", // 154
	"TDWM Delay Request Change", // 155
	"TDWM Summary", // 156
	"TDWM Dyn Rules", // 157
	"TDWM Dyn Obj", // 158
	"TDWM WD Assignment", // 159
	"TDWM Dyn Build", // 160
	"TDWM List WD", // 161
	"Set Session Characteristics As Transaction Isolation Level", // 162
	"Initiate Index Analysis", // 163
	"Replace Authorization", // 164
	"Set Query_Band", // 165
	"Logging Online Archive On", // 166
	"Logging Online Archive Off", // 167
	"Monitor QueryBand", // 168
	"Create Correlation", // 169
	"Replace Correlation", // 170
	"Drop Correlation", // 171
	"Alter Correlation", // 172
	"User Event Control", // 173
	"Event Status", // 174
	"Monitor AWT Resource", // 175
	"SP Dynamic Result Set", // 176
	"Create Replication Rule Set", // 177
	"Replace Replication Rule Set", // 178
	"Drop Replication Rule Set", // 179
	"Create Operator", // 180
	"Replace Operator", // 181
	"Rename Operator", // 182
	"Drop Operator", // 183
	"Grant Connect Through", // 184
	"Revoke Connect Through", // 185
	"Create GLOP Set", // 186
	"Drop GLOP Set", // 187
	"Create Security Constraint", // 188
	"Alter Security Constraint", // 189
	"Drop Security Constraint", // 190
	"Create Index Type", // 191
	"Drop Index Type", // 192
	"Replace Index Type", // 193
	"Alter Index", // 194
	"Check Workload For", // 195
	"FastExport No Spooling", // 196
	"Check Workload End", // 197
	"Flush DBQL Cache", // 198
	"TDWM Exceptions", // 199
	"TDWM Test", // 200
	"Monitor TDWM Resource", // 201
	"Monitor WD Resource", // 202
	"", // 203
	"Set Session Calendar", // 204
	"Monitor Request", // 205
	"Merge Delete", // 206
	"Begin Query Capture", // 207
	"End Query Capture", // 208
	"Show In XML", // 209
	"", // 210
	"", // 211
	"", // 212
	"Set Session Characteristics VeryHot", // 213
	"Unity SQL Statement", // 214
	"Set Session SearchUIFDBPath", // 215
	"Create Zone", // 216
	"Alter Zone", // 217
	"Drop Zone", // 218
	"Create Foreign Server", // 219
	"Alter Foreign Server", // 220
	"Drop Foreign Server", // 221
	"Begin Isolated Load", // 222
	"Checkpoint Isolated Load", // 223
	"End Isolated Load", // 224
	"Set Session For Load", // 225
	"Grant Zone", // 226
	"Revoke Zone", // 227
	"ForViewpoint", // 228
	"Set Session JSON Ignore", // 229
	"Initiate Check", // 230
	"Create Map", // 231
	"Set Session Character Set Unicode Pass Through", // 232
	"Drop Map", // 233
	"Grant Map ", // 234
	"Revoke Map", // 235
	"Set Session Dot Notation", // 236
	"Create Dataset Schema", // 237
	"Drop Dataset Schema", // 238
	"Set Session Debug", // 239
	"Set Foreign Server", // 240
	"Set Transform Group", // 241
	"Create Function Mapping", // 242
	"Replace Function Mapping", // 243
	"Drop Function Mapping", // 244
	"Incremental Restore Allow Write", // 245
	"Execute Function Into", // 246
	"Set Session UDFSearchPath", // 247
	"Select Out Table", // 248
	"Insert Select Out Table", // 249
	"Execute Function", // 250

} // end ACTIVITY

func messagesResultSet (uActivityType uint16) bool {

	switch (uActivityType) {

		case 6, // Create Table
			7, // Modify Table
			8, // Create View
			9, // Create Macro
			10, // Drop Table
			11, // Drop View
			12, // Drop Macro
			13, // Drop Index
			14, // Rename Table
			15, // Rename View
			16, // Rename Macro
			17, // Create Index
			18, // Create Database
			19, // Create User
			23, // Drop Database
			24, // Modify Database
			34, // Replace View
			35, // Replace Macro
			46, // Delete Database
			97, // Create Trigger
			98, // Drop Trigger
			99, // Rename Trigger
			100, // Replace Trigger
			101, // Alter Trigger
			103, // Drop Procedure
			104, // Create procedure
			106, // Rename Procedure
			107, // Replace procedure
			114, // Create Role
			115, // Drop Role
			118, // Create Profile
			119, // Modify Profile
			120, // Drop Profile
			122, // Create Function
			123, // Replace Function
			124, // Drop Function
			125, // Alter Function
			126, // Rename Function
			130, // Alter Procedure
			134, // Create Type
			135, // Drop Type
			136, // Alter Type
			137, // Replace Type
			138, // Create Method
			139, // Alter Method
			140, // Replace Method
			141, // Create Cast
			142, // Replace Cast
			143, // Drop Cast
			144, // Create Ordering
			145, // Replace Ordering
			146, // Drop Ordering
			147, // Create Transform
			148, // Replace Transform
			149, // Drop Transform
			150, // Create Authorization
			151, // Drop Authorization
			154, // Drop Replication Group
			164, // Replace Authorization
			169, // Create Correlation
			170, // Replace Correlation
			171, // Drop Correlation
			172, // Alter Correlation
			177, // Create Replication Rule Set
			178, // Replace Replication Rule Set
			179, // Drop Replication Rule Set
			180, // Create Operator
			181, // Replace Operator
			182, // Rename Operator
			183, // Drop Operator
			186, // Create GLOP Set
			187, // Drop GLOP Set
			188, // Create Security Constraint
			189, // Alter Security Constraint
			190, // Drop Security Constraint
			191, // Create Index Type
			192, // Drop Index Type
			193, // Replace Index Type
			194, // Alter Index
			216, // Create Zone
			217, // Alter Zone
			218, // Drop Zone
			219, // Create Foreign Server
			220, // Alter Foreign Server
			221, // Drop Foreign Server
			231, // Create Map
			233, // Drop Map
			237, // Create Dataset Schema
			238, // Drop Dataset Schema
			242, // Create Function Mapping
			243, // Replace Function Mapping
			244: // Drop Function Mapping

			return true
	}

	return false

} // end messagesResultSet

func resultSetExpected (uActivityType uint16) bool {

	switch (uActivityType) {

		case 1, // Select
			40, // HUTConfig
			49, // Show
			50, // Help
			51, // Begin Loading
			105, // Call Procedure
			122, // Create Function
			123, // Replace Function
			152, // Create Replication Group
			153, // Alter Replication Group
			176, // Dynamic Result set
			196, // FastExport NoSpool
			197, // Check Workload End
			209, // Show In XML
			230, // Initiate Check
			246, // Execute Function Into
			248, // Select Out Table
			250: // Execute Function

			return true
	}

	return false

} // end resultSetExpected

func warningIsReallyError (uCode uint16) bool {

	switch (uCode) {

		case 5526, // Stored Procedure is not created/replaced due to error(s).
			5603, // Errors encountered in compiling UDF/XSP/UDM/UDT/JAR.
			7980: // A JAVA method in the specified Jar which matches that in the EXTERNAL NAME clause was not found.

			return true
	}

	return false

} // end warningIsReallyError

func errorIsReallyWarning (uCode uint16) bool {

	switch (uCode) {

		case 3187, // Utility terminated.
			3514, // User-generated transaction ABORT
			3747: // No startup string defined for this user.

			return true
	}

	return false

} // end errorIsReallyWarning

const LAYOUT_TYPE_LIMITED_CONTENT = 2
const LAYOUT_TYPE_STRUCT_CONTENT  = 5

var LAYOUT_TYPE [] string = [] string {

	"", // 0
	"FullContent", // 1
	"LimitedContent", // 2
	"Statistic", // 3
	"EndOfList", // 4
	"StructContent", // 5

} // end LAYOUT_TYPE

var CONTENT_TYPE [] string = [] string {

	"", // 0
	"ParamMarker", // 1
	"RSColumn", // 2
	"RSSummary", // 3
	"AGKColumn", // 4
	"SPParameter", // 5
	"SPRSColumn", // 6
	"EstProcTime", // 7
	"ParamValue", // 8

} // end CONTENT_TYPE

const T_BLOB_AS_INLINE                  uint16 = 400
const T_BLOB_AS_DEFERRED                uint16 = 404
const T_BLOB_AS_LOCATOR                 uint16 = 408
const T_BLOB_AS_DEFERRED_BY_NAME        uint16 = 412
const T_CLOB_AS_INLINE                  uint16 = 416
const T_CLOB_AS_DEFERRED                uint16 = 420
const T_CLOB_AS_LOCATOR                 uint16 = 424
const T_CLOB_AS_DEFERRED_BY_NAME        uint16 = 428
const T_STRUCT                          uint16 = 440
const T_VARCHAR                         uint16 = 448
const T_CHAR                            uint16 = 452
const T_LONG_VARCHAR                    uint16 = 456
const T_VARGRAPHIC                      uint16 = 464
const T_GRAPHIC                         uint16 = 468
const T_LONG_VARGRAPHIC                 uint16 = 472
const T_FLOAT                           uint16 = 480
const T_DECIMAL                         uint16 = 484
const T_INTEGER                         uint16 = 496
const T_SMALLINT                        uint16 = 500
const T_ARRAY_1D                        uint16 = 504
const T_ARRAY_ND                        uint16 = 508
const T_DATASET_AVRO                    uint16 = 512
const T_DATASET_CSV                     uint16 = 516
const T_BIGINT                          uint16 = 600
const T_NUMBER                          uint16 = 604
const T_VARBYTE                         uint16 = 688
const T_BYTE                            uint16 = 692
const T_LONG_VARBYTE                    uint16 = 696
const T_DATE_ANSI                       uint16 = 748
const T_DATE_INTEGER                    uint16 = 752
const T_BYTEINT                         uint16 = 756
const T_TIME                            uint16 = 760
const T_TIMESTAMP                       uint16 = 764
const T_TIME_WITH_TIME_ZONE             uint16 = 768
const T_TIMESTAMP_WITH_TIME_ZONE        uint16 = 772
const T_INTERVAL_YEAR                   uint16 = 776
const T_INTERVAL_YEAR_TO_MONTH          uint16 = 780
const T_INTERVAL_MONTH                  uint16 = 784
const T_INTERVAL_DAY                    uint16 = 788
const T_INTERVAL_DAY_TO_HOUR            uint16 = 792
const T_INTERVAL_DAY_TO_MINUTE          uint16 = 796
const T_INTERVAL_DAY_TO_SECOND          uint16 = 800
const T_INTERVAL_HOUR                   uint16 = 804
const T_INTERVAL_HOUR_TO_MINUTE         uint16 = 808
const T_INTERVAL_HOUR_TO_SECOND         uint16 = 812
const T_INTERVAL_MINUTE                 uint16 = 816
const T_INTERVAL_MINUTE_TO_SECOND       uint16 = 820
const T_INTERVAL_SECOND                 uint16 = 824
const T_PERIOD_DATE                     uint16 = 832
const T_PERIOD_TIME                     uint16 = 836
const T_PERIOD_TIME_WITH_TIME_ZONE      uint16 = 840
const T_PERIOD_TIMESTAMP                uint16 = 844
const T_PERIOD_TIMESTAMP_WITH_TIME_ZONE uint16 = 848
const T_XML                             uint16 = 852
const T_XML_AS_DEFERRED                 uint16 = 856
const T_XML_AS_LOCATOR                  uint16 = 860
const T_JSON                            uint16 = 880
const T_JSON_AS_LOCATOR                 uint16 = 884
const T_JSON_AS_DEFERRED                uint16 = 888

func byteCountFromDecimalPrecision (uPrecision uint8) uint64 {

	switch {
		case uPrecision <=  2 : return  1
		case uPrecision <=  4 : return  2
		case uPrecision <=  9 : return  4
		case uPrecision <= 18 : return  8
		default               : return 16
	}
} // end func byteCountFromDecimalPrecision

func decimalPrecisionFromByteCount (uByteCount uint64) uint8 {

	switch {
		case uByteCount == 1 : return  2
		case uByteCount == 2 : return  4
		case uByteCount <= 4 : return  9
		case uByteCount <= 8 : return 18
		default              : return 38
	}
} // end func decimalPrecisionFromByteCount

func dataTypeInfo (uCookedDataType uint16) (int, bool, bool, bool, string) {

	switch uCookedDataType {

		// len indicates the byte count of the data value length prefix; zero indicates no length prefix.
		// var indicates whether the data type is known to be variable length.
		// prec indicates whether StatementInfo parcel provides precision and scale for the data type.
		// bind indicates whether the data type is supported as a bind value.

		//                                             len var    prec   bind    name
		case T_BLOB_AS_INLINE                  : return 8, true , false, false, "BLOB" // AS INLINE
		case T_BLOB_AS_DEFERRED                : return 0, false, false, true , "BLOB" // AS DEFERRED
		case T_BLOB_AS_LOCATOR                 : return 2, true , false, true , "LOCATOR(BLOB)"
		case T_BLOB_AS_DEFERRED_BY_NAME        : return 0, true , false, false, "BLOB" // AS DEFERRED BY NAME
		case T_CLOB_AS_INLINE                  : return 8, true , false, false, "CLOB" // AS INLINE
		case T_CLOB_AS_DEFERRED                : return 0, false, false, true , "CLOB" // AS DEFERRED
		case T_CLOB_AS_LOCATOR                 : return 2, true , false, true , "LOCATOR(CLOB)"
		case T_CLOB_AS_DEFERRED_BY_NAME        : return 0, true , false, false, "CLOB" // AS DEFERRED BY NAME
		case T_STRUCT                          : return 0, true , false, false, "STRUCT"
		case T_VARCHAR                         : return 2, true , false, true , "VARCHAR"
		case T_CHAR                            : return 0, false, false, true , "CHAR"
		case T_LONG_VARCHAR                    : return 2, true , false, true , "LONG VARCHAR"
		case T_VARGRAPHIC                      : return 2, true , false, false, "VARGRAPHIC"
		case T_GRAPHIC                         : return 0, false, false, false, "GRAPHIC"
		case T_LONG_VARGRAPHIC                 : return 2, true , false, false, "LONG VARGRAPHIC"
		case T_FLOAT                           : return 0, false, false, true , "FLOAT"
		case T_DECIMAL                         : return 0, false, true , true , "DECIMAL"
		case T_INTEGER                         : return 0, false, false, true , "INTEGER"
		case T_SMALLINT                        : return 0, false, false, true , "SMALLINT"
		case T_ARRAY_1D                        : return 2, true , false, false, "ARRAY" // one dimension
		case T_ARRAY_ND                        : return 2, true , false, false, "ARRAY" // multiple dimensions
		case T_BIGINT                          : return 0, false, false, true , "BIGINT"
		case T_NUMBER                          : return 1, true , true , true , "NUMBER"
		case T_VARBYTE                         : return 2, true , false, true , "VARBYTE"
		case T_BYTE                            : return 0, false, false, true , "BYTE"
		case T_LONG_VARBYTE                    : return 2, true , false, true , "LONG VARBYTE"
		case T_DATE_ANSI                       : return 0, false, false, true , "DATE" // DateForm ANSIDate
		case T_DATE_INTEGER                    : return 0, false, false, true , "DATE" // DateForm IntegerDate
		case T_BYTEINT                         : return 0, false, false, true , "BYTEINT"
		case T_TIME                            : return 0, false, true , true , "TIME"
		case T_TIMESTAMP                       : return 0, false, true , true , "TIMESTAMP"
		case T_TIME_WITH_TIME_ZONE             : return 0, false, true , true , "TIME WITH TIME ZONE"
		case T_TIMESTAMP_WITH_TIME_ZONE        : return 0, false, true , true , "TIMESTAMP WITH TIME ZONE"
		case T_INTERVAL_YEAR                   : return 0, false, false, true , "INTERVAL YEAR"
		case T_INTERVAL_YEAR_TO_MONTH          : return 0, false, false, true , "INTERVAL YEAR TO MONTH"
		case T_INTERVAL_MONTH                  : return 0, false, false, true , "INTERVAL MONTH"
		case T_INTERVAL_DAY                    : return 0, false, false, true , "INTERVAL DAY"
		case T_INTERVAL_DAY_TO_HOUR            : return 0, false, false, true , "INTERVAL DAY TO HOUR"
		case T_INTERVAL_DAY_TO_MINUTE          : return 0, false, false, true , "INTERVAL DAY TO MINUTE"
		case T_INTERVAL_DAY_TO_SECOND          : return 0, false, true , true , "INTERVAL DAY TO SECOND"
		case T_INTERVAL_HOUR                   : return 0, false, false, true , "INTERVAL HOUR"
		case T_INTERVAL_HOUR_TO_MINUTE         : return 0, false, false, true , "INTERVAL HOUR TO MINUTE"
		case T_INTERVAL_HOUR_TO_SECOND         : return 0, false, true , true , "INTERVAL HOUR TO SECOND"
		case T_INTERVAL_MINUTE                 : return 0, false, false, true , "INTERVAL MINUTE"
		case T_INTERVAL_MINUTE_TO_SECOND       : return 0, false, true , true , "INTERVAL MINUTE TO SECOND"
		case T_INTERVAL_SECOND                 : return 0, false, true , true , "INTERVAL SECOND"
		case T_PERIOD_DATE                     : return 0, false, false, true , "PERIOD(DATE)"
		case T_PERIOD_TIME                     : return 0, false, true , true , "PERIOD(TIME)"
		case T_PERIOD_TIME_WITH_TIME_ZONE      : return 0, false, true , true , "PERIOD(TIME WITH TIME ZONE)"
		case T_PERIOD_TIMESTAMP                : return 2, false, true , true , "PERIOD(TIMESTAMP)"
		case T_PERIOD_TIMESTAMP_WITH_TIME_ZONE : return 2, false, true , true , "PERIOD(TIMESTAMP WITH TIME ZONE)"
		case T_XML                             : return 8, true , false, true , "XML"
		case T_XML_AS_DEFERRED                 : return 0, true , false, true , "XML" // AS DEFERRED
		case T_XML_AS_LOCATOR                  : return 2, true , false, true , "LOCATOR(XML)"
		case T_JSON                            : return 8, true , false, true , "JSON"
		case T_JSON_AS_LOCATOR                 : return 2, true , false, true , "LOCATOR(JSON)"
		case T_JSON_AS_DEFERRED                : return 0, false, false, true , "JSON" // AS DEFERRED
		case T_DATASET_AVRO                    : return 2, true , false, false, "DATASET STORAGE FORMAT AVRO"
		case T_DATASET_CSV                     : return 2, true , false, false, "DATASET STORAGE FORMAT CSV"

	} // end switch

	return 0, false, false, false, "unknown"

} // end func dataTypeInfo

func makeFeature (uFlavor uint16, aabyInputs ... [] byte) [] byte {

	nBodyLength := 0
	for _, abyInput := range aabyInputs {
		nBodyLength += len (abyInput)
	}

	abyOutput := make ([] byte, 4 + nBodyLength)

	binary.BigEndian.PutUint16 (abyOutput, uFlavor)
	binary.BigEndian.PutUint16 (abyOutput [2 : ], uint16 (nBodyLength))

	copyByteSlicesInto (abyOutput [4 : ], aabyInputs)

	return abyOutput

} // end func makeFeature

func makeParcel (uFlavor uint16, aabyInputs ... [] byte) [] byte {

	return makeParcelEx (/*bForceAPH*/ false, uFlavor, aabyInputs ...)

} // end func makeParcel

func makeParcelEx (bForceAPH bool, uFlavor uint16, aabyInputs ... [] byte) [] byte {

	nBodyLength := 0
	for _, abyInput := range aabyInputs {
		nBodyLength += len (abyInput)
	}

	nParcelHeaderLength := 4
	nParcelLength       := nParcelHeaderLength + nBodyLength
	bAPH                := bForceAPH || nParcelLength > 0xFFFF
	if (bAPH) {
		uFlavor             |= 0x8000
		nParcelHeaderLength += 4
		nParcelLength       += 4
	}

	abyOutput := make ([] byte, nParcelLength)

	binary.BigEndian.PutUint16 (abyOutput, uFlavor)

	if (bAPH) {
		binary.BigEndian.PutUint32 (abyOutput [4 : ], uint32 (nParcelLength))
	} else {
		binary.BigEndian.PutUint16 (abyOutput [2 : ], uint16 (nParcelLength))
	}

	copyByteSlicesInto (abyOutput [nParcelHeaderLength : ], aabyInputs)

	return abyOutput

} // end func makeParcelEx

func altHeaderRequired (aabyInputs ... [] byte) (bAPH bool) {

	nBodyLength := 0
	for _, abyInput := range aabyInputs {
		nBodyLength += len (abyInput)
	}

	nParcelHeaderLength := 4
	nParcelLength       := nParcelHeaderLength + nBodyLength
	bAPH                 = nParcelLength > 0xFFFF
	return

} // end func altHeaderRequired

func getByteVar (uMessageKind byte) byte {

	switch uMessageKind {

		case MESSAGE_KIND_CONFIG,
			MESSAGE_KIND_ASSIGN,
			MESSAGE_KIND_REASSIGN,
			MESSAGE_KIND_SSO:
			return 7 // AT&T 3B2 (big endian)

		case MESSAGE_KIND_RECONNECT:
			return 1 // Start Request was sent, but Start Response was not received

		default:
			return 0
	}
} // end func getByteVar

const CHARSET_CODE_ASCII byte = 0xFF
const CHARSET_CODE_UTF8  byte = 0xBF
const CHARSET_CODE_UTF16 byte = 0xBE // big-endian UTF16

func makeMessage (uMessageClass byte, uMessageKind byte, uSessionNumber uint32, uAuthenticationNonce uint64, uRequestNumber uint32, uCharsetCode byte, aabyParcels ... [] byte) [] byte {

	nBodyLength          := combinedLength (aabyParcels)
	uHighOrderBodyLength := uint16 (nBodyLength >> 16)
	uLowOrderBodyLength  := uint16 (nBodyLength)

	abyOutput := make ([] byte, 52 + nBodyLength)

	abyOutput                             [ 0   ] = 3 // Message Version: 3
	abyOutput                             [ 1   ] = uMessageClass
	abyOutput                             [ 2   ] = uMessageKind
	binary.BigEndian.PutUint16 (abyOutput [ 3 : ] , uHighOrderBodyLength)
	abyOutput                             [ 5   ] = getByteVar (uMessageKind)
	binary.BigEndian.PutUint16 (abyOutput [ 8 : ] , uLowOrderBodyLength)
	binary.BigEndian.PutUint32 (abyOutput [20 : ] , uSessionNumber)
	binary.BigEndian.PutUint64 (abyOutput [24 : ] , uAuthenticationNonce)
	binary.BigEndian.PutUint32 (abyOutput [32 : ] , uRequestNumber)
	abyOutput                             [37   ] = uCharsetCode

	copyByteSlicesInto (abyOutput [52 : ], aabyParcels)

	return abyOutput

} // end func makeMessage

func remakeEncryptedRequestMessage (abyRequestMsg [] byte, abyEncryptedPortion [] byte) [] byte {

	abyRequestMsg = append (abyRequestMsg [ : 24], abyEncryptedPortion ...) // combine the unencrypted portion with the encrypted portion

	abyRequestMsg [1] |= 0x80 // set the encrypted bit in the message header

	// Recalculate the message body length and replace it into the message header

	nBodyLength          := len (abyRequestMsg) - 52
	uHighOrderBodyLength := uint16 (nBodyLength >> 16)
	uLowOrderBodyLength  := uint16 (nBodyLength)

	binary.BigEndian.PutUint16 (abyRequestMsg [3 : ], uHighOrderBodyLength)
	binary.BigEndian.PutUint16 (abyRequestMsg [8 : ], uLowOrderBodyLength)

	return abyRequestMsg

} // end remakeEncryptedRequestMessage

func remakeDecryptedResponseMessage (abyResponseMsg [] byte, abyDecryptedPortion [] byte) [] byte {

	abyResponseMsg = append (abyResponseMsg [ : 24], abyDecryptedPortion ...) // combine the unencrypted portion with the encrypted portion

	abyResponseMsg [1] &= 0x7F // clear the encrypted bit in the message header

	// Recalculate the message body length and replace it into the message header

	nBodyLength          := len (abyResponseMsg) - 52
	uHighOrderBodyLength := uint16 (nBodyLength >> 16)
	uLowOrderBodyLength  := uint16 (nBodyLength)

	binary.BigEndian.PutUint16 (abyResponseMsg [3 : ], uHighOrderBodyLength)
	binary.BigEndian.PutUint16 (abyResponseMsg [8 : ], uLowOrderBodyLength)

	return abyResponseMsg

} // end remakeDecryptedResponseMessage

func inTransaction (abyResponseMsg [] byte) bool {

	return (abyResponseMsg [5] & 1) != 0

} // end func inTransaction

func rawParcelFlavor (abyParcelHeader [] byte) uint16 {

	return binary.BigEndian.Uint16 (abyParcelHeader)

} // end func rawParcelFlavor

func cookedParcelFlavor (abyParcelHeader [] byte) uint16 {

	return rawParcelFlavor (abyParcelHeader) & 0x7FFF

} // end func cookedParcelFlavor

func isAPH (abyParcelHeader [] byte) bool {

	return (rawParcelFlavor (abyParcelHeader) & 0x8000) != 0

} // end func isAPH

func parcelLength (abyParcelHeader [] byte) uint32 {

	if isAPH (abyParcelHeader) {
		return binary.BigEndian.Uint32 (abyParcelHeader [4 : ])
	} else {
		return uint32 (binary.BigEndian.Uint16 (abyParcelHeader [2 : ]))
	}
} // end parcelLength

func parcelHeaderLength (abyParcelHeader [] byte) uint32 {

	if isAPH (abyParcelHeader) {
		return 8
	} else {
		return 4
	}
} // end func parcelHeaderLength

func parcelBodyLength (abyParcelHeader [] byte) uint32 {

	return parcelLength (abyParcelHeader) - parcelHeaderLength (abyParcelHeader)

} // end func parcelBodyLength

func constrainedParcelBody (abyParcelHeader [] byte) [] byte {

	return abyParcelHeader [parcelHeaderLength (abyParcelHeader) : parcelLength (abyParcelHeader)]

} // end func constrainedParcelBody

func remainingBytesAfterParcel (abyParcelHeader [] byte) int {

	return len (abyParcelHeader) - int (parcelLength (abyParcelHeader))
}

func hasNextParcel (abyParcelHeader [] byte) bool {

	return remainingBytesAfterParcel (abyParcelHeader) > 0

} // end func hasNextParcel

func nextParcel (abyParcelHeader [] byte) [] byte {

	return abyParcelHeader [parcelLength (abyParcelHeader) : ]

} // end func nextParcel

func peekNextCookedParcelFlavor (abyParcelHeader [] byte) uint16 {

	if hasNextParcel (abyParcelHeader) {
		return cookedParcelFlavor (nextParcel (abyParcelHeader))
	} else {
		return 0
	}
} // end func peekNextCookedParcelFlavor

func getParcelHeadersFromCurrent (aby [] byte) (aabyParcelHeaders [][] byte) {

	for abyParcelHeader := aby ; ; abyParcelHeader = nextParcel (abyParcelHeader) {

		aabyParcelHeaders = append (aabyParcelHeaders, abyParcelHeader) // returned slices are open-ended and not constrained to parcels

		if ! hasNextParcel (abyParcelHeader) { return }
	}
} // end func getParcelHeadersFromCurrent

func getParcelHeadersFromMessage (abyMessage [] byte) [][] byte {

	return getParcelHeadersFromCurrent (abyMessage [52 : ])

} // end func getParcelHeadersFromMessage

func peekCookedParcelFlavor (iParcelHeader int, aabyParcelHeaders [][] byte) uint16 {

	if len (aabyParcelHeaders) > iParcelHeader {
		return cookedParcelFlavor (aabyParcelHeaders [iParcelHeader])
	} else {
		return 0
	}
} // end func peekCookedParcelFlavor

func cookedDataType (uRawDataType uint16) uint16 {

	// The cooked data type is the non-nullable data type code.
	// nullable type code    = cooked data type + 1
	// sproc IN    parameter = cooked data type + 500
	// sproc INOUT parameter = cooked data type + 501
	// sproc OUT   parameter = cooked data type + 502

	if uRawDataType >= 900 {
		uRawDataType -= 500
	}

	return uRawDataType & 0xFFFC // mask off the 2-bit and 1-bit

} // end func cookedDataType

func deferredLobDataType (uCookedDataType uint16) uint16 {

	// We must use a deferred LOB to insert VARCHAR with length > 32000 or VARBYTE with length > 64000.
	// This function returns the deferred LOB data type code corresponding to the original data type code.
	// VARBYTE (688) = DEFERRED BLOB (404)
	// VARCHAR (448) = DEFERRED CLOB (420)
	// JSON    (880) = DEFERRED JSON (888)
	// XML     (852) = DEFERRED XML  (856)

	switch uCookedDataType {
		case T_VARBYTE: return T_BLOB_AS_DEFERRED
		case T_VARCHAR: return T_CLOB_AS_DEFERRED
		case T_JSON   : return T_JSON_AS_DEFERRED
		case T_XML    : return T_XML_AS_DEFERRED
	}

	return uCookedDataType

} // end func deferredLobDataType

func isDeferredLobType (uCookedDataType uint16) bool {

	switch uCookedDataType {
		case T_BLOB_AS_DEFERRED,
			T_CLOB_AS_DEFERRED,
			T_JSON_AS_DEFERRED,
			T_XML_AS_DEFERRED: return true
	}

	return false

} // end func isDeferredLobType

func isNullableDataType (uRawDataType uint16) bool {

	return uRawDataType >= 900 || (uRawDataType % 4) == 1

} // end func isNullableDataType

func isInParam (uRawDataType uint16) bool {

	return uRawDataType >= 900 && (uRawDataType % 4) == 0

} // end func isInParam

func skipByte (paby * [] byte) {

	*paby = (*paby) [1 : ]

} // end func skipByte

func skipShort (paby * [] byte) {

	*paby = (*paby) [2 : ]

} // end func skipShort

func skipInt (paby * [] byte) {

	*paby = (*paby) [4 : ]

} // end func skipInt

func skipLong (paby * [] byte) {

	*paby = (*paby) [8 : ]

} // end func skipLong

func skipBytes (paby * [] byte, uByteCount uint32) {

	*paby = (*paby) [uByteCount : ]

} // end func skipBytes

func consumeByte (paby * [] byte) (by byte) {

	by = (*paby) [0]
	skipByte (paby)
	return

} // end func consumeByte

func consumeShort (paby * [] byte) (u uint16) {

	u = binary.BigEndian.Uint16 (*paby)
	skipShort (paby)
	return

} // end func consumeShort

func consumeInt (paby * [] byte) (u uint32) {

	u = binary.BigEndian.Uint32 (*paby)
	skipInt (paby)
	return

} // end func consumeInt

func consumeLong (paby * [] byte) (u uint64) {

	u = binary.BigEndian.Uint64 (*paby)
	skipLong (paby)
	return

} // end func consumeLong

func consumeBytes (paby * [] byte, uByteCount uint32) (abyOutput [] byte) {

	abyOutput = (*paby) [ : uByteCount]
	*paby = (*paby) [uByteCount : ]
	return

} // end func consumeBytes

func skipVarField (paby * [] byte) {

	u := consumeShort (paby)
	*paby = (*paby) [u : ]

} // end func skipVarField

func consumeVarbyte (paby * [] byte) [] byte {

	u := consumeShort (paby)
	return consumeBytes (paby, uint32 (u))

} // end consumeVarbyte

func consumeVarcharUTF8 (paby * [] byte) string {

	return string (consumeVarbyte (paby))

} // end consumeVarcharUTF8
