// Copyright 2022 by Teradata Corporation. All Rights Reserved.

//go:build !windows && !wasm && krb5

package goteragss

import (
	"fmt"
	"unsafe"
)

// #cgo LDFLAGS: -ldl
import "C"

/*
#include <stdlib.h>
#include <stdint.h>
#include <dlfcn.h>
#include <string.h>
#include <stdio.h>

typedef uint32_t OM_uint32;
typedef void *tdgss_cred_id_t;
typedef void *tdgss_ctx_id_t;
typedef void *tdgss_name_t;
typedef int tdgss_cred_usage_t;
typedef OM_uint32 tdgss_qop_t;

#if defined(__APPLE__) && (defined(__ppc__) || defined(__ppc64__) || defined(__i386__) || defined(__x86_64__))
#pragma pack(push,2)
#endif

typedef struct tdgss_buffer_desc_struct {
	size_t      length;
	void       *value;
} tdgss_buffer_desc, *tdgss_buffer_t;

typedef struct tdgss_OID_desc_struct {
	OM_uint32  length;
	void      *elements;
} tdgss_OID_desc, *tdgss_OID;

typedef struct tdgss_name_elem_desc_struct {
	tdgss_OID         pNameType;
	tdgss_buffer_desc Name;
} tdgss_name_elem_desc, *tdgss_name_elem;

typedef struct tdgss_OID_set_desc_struct {
	size_t     count;
	tdgss_OID  elements;
} tdgss_OID_set_desc, *tdgss_OID_set;

typedef struct tdgss_channel_bindings_struct {
	OM_uint32          initiator_addrtype;
	tdgss_buffer_desc  initiator_address;
	OM_uint32          acceptor_addrtype;
	tdgss_buffer_desc  acceptor_address;
	tdgss_buffer_desc  application_data;
} *tdgss_channel_bindings_t;

#if defined(__APPLE__) && (defined(__ppc__) || defined(__ppc64__) || defined(__i386__) || defined(__x86_64__))
#pragma pack(pop)
#endif

const OM_uint32 TDGSS_GSS_S_UNAVAILABLE = 0x100000;
const OM_uint32 TDGSS_ERR_FTN_NOT_AVAIL_FOR_MECH = 0xe1000067;

const size_t tdgss_buffer_size = sizeof(tdgss_buffer_desc);
const size_t tdgss_OID_size = sizeof(tdgss_OID_desc);
const size_t tdgss_OID_set_size = sizeof(tdgss_OID_set_desc);

void helper_tdgss_OID_desc_free_elements(tdgss_OID oid) {
	free(oid->elements);
}

void helper_tdgss_OID_desc_set_elements(tdgss_OID oid, OM_uint32 l, void *p) {
	oid->length = l;
	oid->elements = p;
}

void helper_tdgss_OID_desc_get_elements(tdgss_OID oid, OM_uint32 *l, char **p) {
	*l = oid->length;
	*p = oid->elements;
}

typedef OM_uint32 (*tdgss_acquire_cred_t)(OM_uint32           *minor_status,
										const tdgss_name_t   desired_name,
										OM_uint32            time_req,
										const tdgss_OID_set  desired_mechs,
										tdgss_cred_usage_t   cred_usage,
										tdgss_cred_id_t     *output_cred_handle,
										tdgss_OID_set       *actual_mechs,
										OM_uint32           *time_rec);

tdgss_acquire_cred_t fp_gss_acquire_cred = NULL;

OM_uint32 tdgss_acquire_cred_wrapper(OM_uint32            *minor_status,
									const tdgss_name_t   desired_name,
									OM_uint32            time_req,
									const tdgss_OID_set  desired_mechs,
									tdgss_cred_usage_t   cred_usage,
									tdgss_cred_id_t     *output_cred_handle,
									tdgss_OID_set       *actual_mechs,
									OM_uint32           *time_rec)
{
	if (fp_gss_acquire_cred == NULL) {
		*minor_status = TDGSS_ERR_FTN_NOT_AVAIL_FOR_MECH;
		return TDGSS_GSS_S_UNAVAILABLE;
	}

	return fp_gss_acquire_cred(minor_status, desired_name, time_req, desired_mechs, cred_usage, output_cred_handle, actual_mechs, time_rec);
}

typedef OM_uint32 (*tdgss_release_cred_t)(OM_uint32 *minor_status, tdgss_cred_id_t *cred_handle);

tdgss_release_cred_t fp_gss_release_cred = NULL;

OM_uint32 tdgss_release_cred_wrapper(OM_uint32 *minor_status, tdgss_cred_id_t *cred_handle)
{
	if (fp_gss_release_cred == NULL) {
		*minor_status = TDGSS_ERR_FTN_NOT_AVAIL_FOR_MECH;
		return TDGSS_GSS_S_UNAVAILABLE;
	}
	return fp_gss_release_cred(minor_status, cred_handle);
}

typedef OM_uint32 (*tdgss_import_name_t) (
							OM_uint32            *minor_status,
							const tdgss_buffer_t  input_name_buffer,
							const tdgss_OID       input_name_type,
							tdgss_name_t         *output_name);

tdgss_import_name_t fp_gss_import_name = NULL;

OM_uint32 tdgss_import_name_wrapper (
							OM_uint32            *minor_status,
							const tdgss_buffer_t  input_name_buffer,
							const tdgss_OID       input_name_type,
							tdgss_name_t         *output_name)
{
	if (fp_gss_import_name == NULL) {
		*minor_status = TDGSS_ERR_FTN_NOT_AVAIL_FOR_MECH;
		return TDGSS_GSS_S_UNAVAILABLE;
	}
	return fp_gss_import_name(minor_status, input_name_buffer, input_name_type, output_name);
}

typedef OM_uint32 (*tdgss_release_name_t) (OM_uint32 *minor_status, tdgss_name_t *name);

tdgss_release_name_t fp_gss_release_name = NULL;

OM_uint32 tdgss_release_name_wrapper (OM_uint32 *minor_status, tdgss_name_t *name)
{
	if (fp_gss_release_name == NULL) {
		*minor_status = TDGSS_ERR_FTN_NOT_AVAIL_FOR_MECH;
		return TDGSS_GSS_S_UNAVAILABLE;
	}
	return fp_gss_release_name(minor_status, name);
}

typedef OM_uint32 (*tdgss_wrap_t) (
				OM_uint32             *minor_status,
				const tdgss_ctx_id_t   context_handle,
				int                    conf_req_flag,
				tdgss_qop_t            qop_req,
				const tdgss_buffer_t   input_message_buffer,
				int                   *conf_state,
				tdgss_buffer_t         output_message_buffer);

tdgss_wrap_t fp_gss_wrap = NULL;

OM_uint32 tdgss_wrap_wrapper (
						OM_uint32             *minor_status,
						const tdgss_ctx_id_t   context_handle,
						int                    conf_req_flag,
						tdgss_qop_t            qop_req,
						const tdgss_buffer_t   input_message_buffer,
						int                   *conf_state,
						tdgss_buffer_t         output_message_buffer)
{
	if (fp_gss_wrap == NULL) {
		*minor_status = TDGSS_ERR_FTN_NOT_AVAIL_FOR_MECH;
		return TDGSS_GSS_S_UNAVAILABLE;
	}
	return fp_gss_wrap(
					minor_status,
					context_handle,
					conf_req_flag,
					qop_req,
					input_message_buffer,
					conf_state,
					output_message_buffer);
}

typedef OM_uint32 (*tdgss_unwrap_t) (
				OM_uint32             *minor_status,
				const tdgss_ctx_id_t   context_handle,
				const tdgss_buffer_t   input_message_buffer,
				tdgss_buffer_t         output_message_buffer,
				int                   *conf_state,
				tdgss_qop_t           *qop_state);

tdgss_unwrap_t fp_gss_unwrap = NULL;

OM_uint32 tdgss_unwrap_wrapper (
				OM_uint32             *minor_status,
				const tdgss_ctx_id_t   context_handle,
				const tdgss_buffer_t   input_message_buffer,
				tdgss_buffer_t         output_message_buffer,
				int                   *conf_state,
				tdgss_qop_t           *qop_state)
{
	if (fp_gss_unwrap == NULL) {
		*minor_status = TDGSS_ERR_FTN_NOT_AVAIL_FOR_MECH;
		return TDGSS_GSS_S_UNAVAILABLE;
	}
	return fp_gss_unwrap(
				minor_status,
				context_handle,
				input_message_buffer,
				output_message_buffer,
				conf_state,
				qop_state);
}

typedef OM_uint32 (*tdgss_delete_sec_context_t) (
				OM_uint32      *minor_status,
				tdgss_ctx_id_t *context_handle,
				tdgss_buffer_t  output_token);

tdgss_delete_sec_context_t fp_gss_delete_sec_context = NULL;

OM_uint32 tdgss_delete_sec_context_wrapper (
				OM_uint32      *minor_status,
				tdgss_ctx_id_t *context_handle,
				tdgss_buffer_t  output_token)
{
	if (fp_gss_delete_sec_context == NULL) {
		*minor_status = TDGSS_ERR_FTN_NOT_AVAIL_FOR_MECH;
		return TDGSS_GSS_S_UNAVAILABLE;
	}
	return fp_gss_delete_sec_context(minor_status, context_handle, output_token);

}

typedef OM_uint32 (*tdgss_release_buffer_t) (OM_uint32 *minor_status, tdgss_buffer_t buffer);

tdgss_release_buffer_t fp_gss_release_buffer = NULL;

OM_uint32 tdgss_release_buffer_wrapper (OM_uint32 *minor_status, tdgss_buffer_t buffer)
{
	if (fp_gss_release_buffer == NULL) {
		*minor_status = TDGSS_ERR_FTN_NOT_AVAIL_FOR_MECH;
		return TDGSS_GSS_S_UNAVAILABLE;
	}
	return fp_gss_release_buffer(minor_status, buffer);
}

typedef OM_uint32 (*tdgss_init_sec_context_t) (
					OM_uint32                     *minor_status,
					const tdgss_cred_id_t          initiator_cred_handle,
					tdgss_ctx_id_t                *context_handle,
					const tdgss_name_t             target_name,
					const tdgss_OID                mech_type,
					OM_uint32                      req_flags,
					OM_uint32                      time_req,
					const tdgss_channel_bindings_t input_chan_bindings,
					const tdgss_buffer_t           input_token,
					tdgss_OID                     *actual_mech_type,
					tdgss_buffer_t                 output_token,
					OM_uint32                     *ret_flags,
					OM_uint32                     *time_rec);

tdgss_init_sec_context_t fp_gss_init_sec_context = NULL;

OM_uint32 tdgss_init_sec_context_wrapper (
					OM_uint32                     *minor_status,
					const tdgss_cred_id_t          initiator_cred_handle,
					tdgss_ctx_id_t                *context_handle,
					const tdgss_name_t             target_name,
					const tdgss_OID                mech_type,
					OM_uint32                      req_flags,
					OM_uint32                      time_req,
					const tdgss_channel_bindings_t input_chan_bindings,
					const tdgss_buffer_t           input_token,
					tdgss_OID                     *actual_mech_type,
					tdgss_buffer_t                 output_token,
					OM_uint32                     *ret_flags,
					OM_uint32                     *time_rec)
{
	if (fp_gss_init_sec_context == NULL) {
		*minor_status = TDGSS_ERR_FTN_NOT_AVAIL_FOR_MECH;
		return TDGSS_GSS_S_UNAVAILABLE;
	}
	return fp_gss_init_sec_context (
					minor_status,
					initiator_cred_handle,
					context_handle,
					target_name,
					mech_type,
					req_flags,
					time_req,
					input_chan_bindings,
					input_token,
					actual_mech_type,
					output_token,
					ret_flags,
					time_rec);
}

void initialize_function_pointers (void *pLibHandle) // assumes pLibHandle will never be NULL
{
	fp_gss_acquire_cred = dlsym(pLibHandle, "gss_acquire_cred");
	fp_gss_release_cred = dlsym(pLibHandle, "gss_release_cred");
	fp_gss_import_name = dlsym(pLibHandle, "gss_import_name");
	fp_gss_release_name = dlsym(pLibHandle, "gss_release_name");
	fp_gss_wrap = dlsym(pLibHandle, "gss_wrap");
	fp_gss_unwrap = dlsym(pLibHandle, "gss_unwrap");
	fp_gss_init_sec_context = dlsym(pLibHandle, "gss_init_sec_context");
	fp_gss_delete_sec_context = dlsym(pLibHandle, "gss_delete_sec_context");
	fp_gss_release_buffer = dlsym(pLibHandle, "gss_release_buffer");
}

*/
import "C"

const (
	ALLOCTYPE_NONE = iota
	ALLOCTYPE_MALLOC
	ALLOCTYPE_GSSAPI
)

type tdgssBuffer struct {
	allocType int
	C_tdgss_buffer_t C.tdgss_buffer_t
}

type tdgssName struct {
	C_tdgss_name_t C.tdgss_name_t
}

type tdgssOID struct {
	C_tdgss_OID C.tdgss_OID
}

type tdgssCredId struct {
	C_tdgss_cred_id_t C.tdgss_cred_id_t
}

type tdgssCtxId struct {
	C_tdgss_ctx_id_t C.tdgss_ctx_id_t
}

type tdgssOIDSet struct {
	C_tdgss_OID_set C.tdgss_OID_set
}

func NewTdgssOID(data []byte) (newOid *tdgssOID) {
	newOid = &tdgssOID{}

	if len(data) == 0 {
		return
	}

	tdgssOidDesc := C.malloc(C.size_t(C.tdgss_OID_size))
	C.memset(tdgssOidDesc, 0, C.size_t(C.tdgss_OID_size))

	oidBytes := C.CBytes(data)

	newOid.C_tdgss_OID = C.tdgss_OID(tdgssOidDesc)

	// Because of the alignment issues we cannot access tdgss_OID elements fields from go.
	// Using C function to do the same as: newOid.C_tdgss_OID.length = C.OM_uint32(len(data))
	//                                     newOid.C_tdgss_OID.elements = oidBytes
	C.helper_tdgss_OID_desc_set_elements(newOid.C_tdgss_OID, C.OM_uint32(len(data)), oidBytes)

	return
}

func (oid *tdgssOID) Release() {
	if oid.C_tdgss_OID != nil {

		// Because of the alignment issues we cannot access tdgss_OID elements fields from go.
		// Using C function to do the same as: C.free(unsafe.Pointer(oid.C_tdgss_OID.elements))
		//                                     C.free(unsafe.Pointer(oid.C_tdgss_OID))
		C.helper_tdgss_OID_desc_free_elements(oid.C_tdgss_OID)
		oid.C_tdgss_OID = nil
	}
}

func (oid *tdgssOID) Bytes() []byte {
	if oid == nil || oid.C_tdgss_OID == nil || oid.C_tdgss_OID.length == 0 {
		return make([]byte, 0)
	}

	// Because of the alignment issues we cannot access tdgss_OID elements fields from go.
	// Using C function to do the same as: var oidLen C.OM_uint32 = C.size_t(oid.C_tdgss_OID.length)
	//                                     var data *C.char = (*C.char)(oid.C_tdgss_OID.elements)
	var oidLen C.OM_uint32
	var data *C.char
	C.helper_tdgss_OID_desc_get_elements(oid.C_tdgss_OID, &oidLen, &data)

	return C.GoBytes(unsafe.Pointer(data), C.int(oidLen))
}

func NewTdgssName() (newName *tdgssName) {
	newName = &tdgssName{}
	return
}

func (name *tdgssName) Release() {
	if name.C_tdgss_name_t != nil {
		var min C.OM_uint32
		C.tdgss_release_name_wrapper(&min, &name.C_tdgss_name_t)
		name.C_tdgss_name_t = nil
	}
}

func NewTdgssBuffer(allocType int) (newBuffer *tdgssBuffer){
	newBuffer = &tdgssBuffer{}

	tdgssBufferDesc := C.malloc(C.size_t(C.tdgss_buffer_size))
	C.memset(tdgssBufferDesc, 0, C.size_t(C.tdgss_buffer_size))
	newBuffer.C_tdgss_buffer_t = C.tdgss_buffer_t(tdgssBufferDesc)
	newBuffer.allocType = allocType

	return
}

func NewTdgssBufferFromBytes(data []byte) (newBuffer *tdgssBuffer){
	newBuffer = &tdgssBuffer{}

	tdgssBufferDesc := C.malloc(C.size_t(C.tdgss_buffer_size))
	C.memset(tdgssBufferDesc, 0, C.size_t(C.tdgss_buffer_size))
	newBuffer.C_tdgss_buffer_t = C.tdgss_buffer_t(tdgssBufferDesc)

	if len(data) > 0 {
		bufferBytes := C.CBytes(data)
		newBuffer.C_tdgss_buffer_t.length = C.size_t(len(data))
		newBuffer.C_tdgss_buffer_t.value = bufferBytes
		newBuffer.allocType = ALLOCTYPE_MALLOC
	}
	return
}

func NewTdgssBufferFromString(data string) (newBuffer *tdgssBuffer){
	return NewTdgssBufferFromBytes([]byte(data))
}

func (buffer *tdgssBuffer) Release() {
	if buffer.C_tdgss_buffer_t != nil {
		if buffer.C_tdgss_buffer_t.value != nil {
			if buffer.allocType == ALLOCTYPE_GSSAPI {
				var min C.OM_uint32
				C.tdgss_release_buffer_wrapper(&min, buffer.C_tdgss_buffer_t)
			} else {
				C.free(unsafe.Pointer(buffer.C_tdgss_buffer_t.value))
			}
		}
		C.free(unsafe.Pointer(buffer.C_tdgss_buffer_t))
		buffer.C_tdgss_buffer_t = nil
	}
}

func (buffer *tdgssBuffer) Bytes() []byte {
	if buffer == nil || buffer.C_tdgss_buffer_t == nil || buffer.C_tdgss_buffer_t.length == 0 {
		return make([]byte, 0)
	}

	var bufferLen C.size_t = buffer.C_tdgss_buffer_t.length
	var data *C.char = (*C.char)(buffer.C_tdgss_buffer_t.value)
	return C.GoBytes(unsafe.Pointer(data), C.int(bufferLen))
}

func (buffer *tdgssBuffer) String() string {
	if buffer == nil || buffer.C_tdgss_buffer_t == nil || buffer.C_tdgss_buffer_t.length == 0 {
		return ""
	}

	var bufferLen C.size_t = buffer.C_tdgss_buffer_t.length
	var data *C.char = (*C.char)(buffer.C_tdgss_buffer_t.value)
	return C.GoStringN(data, C.int(bufferLen))
}

func NewTdgssOIDSet(abyOid []byte) (oidSet *tdgssOIDSet) {
	oidSet = &tdgssOIDSet{}

	if len(abyOid) == 0 {
		return
	}

	tdgssOIDSetDesc := C.malloc(C.size_t(C.tdgss_OID_set_size))
	C.memset(tdgssOIDSetDesc, 0, C.size_t(C.tdgss_OID_set_size))
	oidSet.C_tdgss_OID_set = C.tdgss_OID_set(tdgssOIDSetDesc)

	tdgssOidDesc := C.malloc(C.size_t(C.tdgss_OID_size))
	C.memset(tdgssOidDesc, 0, C.size_t(C.tdgss_OID_size))

	oidBytes := C.CBytes(abyOid)

	// Because of the alignment issues we cannot access tdgss_OID elements fields from go.
	// Using C function to do the same as: C.tdgss_OID(tdgssOidDesc).length = C.OM_uint32(len(abyOid))
	//                                     C.tdgss_OID(tdgssOidDesc).elements = oidBytes
	C.helper_tdgss_OID_desc_set_elements(C.tdgss_OID(tdgssOidDesc), C.OM_uint32(len(abyOid)), oidBytes)

	oidSet.C_tdgss_OID_set.count = 1
	oidSet.C_tdgss_OID_set.elements = C.tdgss_OID(tdgssOidDesc)
	return
}

func (oidSet *tdgssOIDSet) OidBytes() []byte{
	if oidSet.C_tdgss_OID_set == nil  || oidSet.C_tdgss_OID_set.elements == nil {
			return make([]byte, 0)
	}

	var tdgssOidDesc C.tdgss_OID = oidSet.C_tdgss_OID_set.elements

	// Because of the alignment issues we cannot access tdgss_OID elements fields from go.
	// Using C function to do the same as: var oidLen C.OM_uint32 = C.size_t(tdgssOidDesc.length)
	//                                     var data *C.char = (*C.char)(tdgssOidDesc.elements)
	var oidLen C.OM_uint32
	var data *C.char
	C.helper_tdgss_OID_desc_get_elements(tdgssOidDesc, &oidLen, &data)

	return C.GoBytes(unsafe.Pointer(data), C.int(oidLen))
}

func (oidSet *tdgssOIDSet) Release() {
	if oidSet.C_tdgss_OID_set != nil {
		if oidSet.C_tdgss_OID_set.elements != nil {
			oid := C.tdgss_OID(oidSet.C_tdgss_OID_set.elements)
			// Because of the alignment issues we cannot access tdgss_OID elements fields from go.
			// Using C function to do the same as: C.free(unsafe.Pointer(oid.elements))
			//                                     C.free(unsafe.Pointer(oid))
			C.helper_tdgss_OID_desc_free_elements(oid)
		}
		C.free(unsafe.Pointer(oidSet.C_tdgss_OID_set))
		oidSet.C_tdgss_OID_set = nil
	}
}

func gss_acquire_cred(minor_status       *uint32,
					desired_name         *tdgssName,
					time_req              uint32,
					desired_mechs        *tdgssOIDSet,
					output_cred_handle   *tdgssCredId,
					time_rec             *uint32) uint32 {

	return uint32(C.tdgss_acquire_cred_wrapper(
		(*C.OM_uint32)(unsafe.Pointer(minor_status)),
		desired_name.C_tdgss_name_t,
		C.OM_uint32(time_req),
		desired_mechs.C_tdgss_OID_set,
		GSS_C_INITIATE,
		&output_cred_handle.C_tdgss_cred_id_t,
		nil,
		(*C.OM_uint32)(unsafe.Pointer(time_rec))))
}

func gss_release_cred(minor_status *uint32, cred_handle *tdgssCredId) uint32 {

	return uint32(C.tdgss_release_cred_wrapper(
		(*C.OM_uint32)(unsafe.Pointer(minor_status)),
		&cred_handle.C_tdgss_cred_id_t))
}

func NewTdgssCredId() *tdgssCredId {
	return &tdgssCredId{}
}

func (cred *tdgssCredId) Release() {
	if cred.C_tdgss_cred_id_t != nil {
		var min uint32
		gss_release_cred(&min, cred)
	}
}

func gss_delete_sec_context(minor_status *uint32, ctxId *tdgssCtxId) uint32 {

	return uint32(C.tdgss_delete_sec_context_wrapper(
		(*C.OM_uint32)(unsafe.Pointer(minor_status)),
		&ctxId.C_tdgss_ctx_id_t,
		nil))
}

func NewTdgssCtxId() *tdgssCtxId {
	return &tdgssCtxId{}
}

func (ctxId *tdgssCtxId) Release() {
	if ctxId.C_tdgss_ctx_id_t != nil {
		var min uint32
		gss_delete_sec_context(&min, ctxId)
	}
}

func gss_import_name(minor_status *uint32,
				input_name_buffer *tdgssBuffer,
				input_name_type   *tdgssOID,
				output_name       *tdgssName) uint32 {

return uint32(C.tdgss_import_name_wrapper(
	(*C.OM_uint32)(unsafe.Pointer(minor_status)),
	input_name_buffer.C_tdgss_buffer_t,
	input_name_type.C_tdgss_OID,
	&output_name.C_tdgss_name_t))
}

func gss_wrap(minor_status         *uint32,
			context_handle         *tdgssCtxId,
			conf_req_flag           int,
			qop_req                 uint32,
			input_message_buffer   *tdgssBuffer,
			conf_state             *int,
			output_message_buffer  *tdgssBuffer) uint32 {

	return uint32(C.tdgss_wrap_wrapper(
				(*C.OM_uint32)(unsafe.Pointer(minor_status)),
				context_handle.C_tdgss_ctx_id_t,
				C.int(conf_req_flag),
				C.tdgss_qop_t(qop_req),
				input_message_buffer.C_tdgss_buffer_t,
				(*C.int)(unsafe.Pointer(conf_state)),
				output_message_buffer.C_tdgss_buffer_t))
}

func gss_unwrap(minor_status       *uint32,
			context_handle         *tdgssCtxId,
			input_message_buffer   *tdgssBuffer,
			output_message_buffer  *tdgssBuffer,
			conf_state             *int,
			qop_state              *uint32) uint32 {

	return uint32(C.tdgss_unwrap_wrapper(
				(*C.OM_uint32)(unsafe.Pointer(minor_status)),
				context_handle.C_tdgss_ctx_id_t,
				input_message_buffer.C_tdgss_buffer_t,
				output_message_buffer.C_tdgss_buffer_t,
				(*C.int)(unsafe.Pointer(conf_state)),
				nil))

}

func gss_init_sec_context(minor_status   *uint32,
				initiator_cred_handle    *tdgssCredId,
				context_handle           *tdgssCtxId,
				target_name              *tdgssName,
				mech_type                *tdgssOID,
				req_flags                 uint32,
				time_req                  uint32,
				input_token              *tdgssBuffer,
				output_token             *tdgssBuffer,
				ret_flags                *uint32,
				time_rec                 *uint32) uint32 {

	return uint32(C.tdgss_init_sec_context_wrapper(
						(*C.OM_uint32)(unsafe.Pointer(minor_status)),
						initiator_cred_handle.C_tdgss_cred_id_t,
						&context_handle.C_tdgss_ctx_id_t,
						target_name.C_tdgss_name_t,
						mech_type.C_tdgss_OID,
						C.OM_uint32(req_flags),
						C.OM_uint32(time_req),
						nil, //input_chan_bindings
						input_token.C_tdgss_buffer_t,
						nil, //actual_mech_type
						output_token.C_tdgss_buffer_t,
						(*C.OM_uint32)(unsafe.Pointer(ret_flags)),
						(*C.OM_uint32)(unsafe.Pointer(time_rec))))
}

func initialize_function_pointers(logger TdgssLogger, pLibHandle unsafe.Pointer) {

	if logger.HasTraceLog() {
		logger.TraceLog("> enter initialize_function_pointers")
		defer func() { logger.TraceLog("< leave initialize_function_pointers") }()
	}

	C.initialize_function_pointers(pLibHandle)

	if logger.HasDebugLog() {
		logger.DebugLog(fmt.Sprintf("initialize_function_pointers fp_gss_acquire_cred       = %p", C.fp_gss_acquire_cred))
		logger.DebugLog(fmt.Sprintf("initialize_function_pointers fp_gss_release_cred       = %p", C.fp_gss_release_cred))
		logger.DebugLog(fmt.Sprintf("initialize_function_pointers fp_gss_import_name        = %p", C.fp_gss_import_name))
		logger.DebugLog(fmt.Sprintf("initialize_function_pointers fp_gss_release_name       = %p", C.fp_gss_release_name))
		logger.DebugLog(fmt.Sprintf("initialize_function_pointers fp_gss_wrap               = %p", C.fp_gss_wrap))
		logger.DebugLog(fmt.Sprintf("initialize_function_pointers fp_gss_unwrap             = %p", C.fp_gss_unwrap))
		logger.DebugLog(fmt.Sprintf("initialize_function_pointers fp_gss_init_sec_context   = %p", C.fp_gss_init_sec_context))
		logger.DebugLog(fmt.Sprintf("initialize_function_pointers fp_gss_delete_sec_context = %p", C.fp_gss_delete_sec_context))
		logger.DebugLog(fmt.Sprintf("initialize_function_pointers fp_gss_release_buffer     = %p", C.fp_gss_release_buffer))
	}
}
