// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"encoding/base64"
	"fmt"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/agent"
	"github.com/teradata-labs/loom/pkg/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// maxWeaveImages is the most pictures one WeaveRequest may carry.
	maxWeaveImages = 10
	// maxWeaveImageBytes is the largest one picture may be.
	maxWeaveImageBytes = 5 << 20
	// MaxRequestBytes is the largest weave request the server accepts
	// (gRPC message and HTTP body): room for maxWeaveImages pictures as
	// phones send them, made smaller first (gRPC's default is 4 MiB).
	MaxRequestBytes = 32 << 20
)

var weaveImageTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}

// weaveContentBlocks turns WeaveRequest.images into the user turn's content
// blocks, nil when there are none. The query goes first, as text, so the
// model reads the question before the pictures.
func weaveContentBlocks(req *loomv1.WeaveRequest) ([]agent.ContentBlock, error) {
	images := req.GetImages()
	if len(images) == 0 {
		return nil, nil
	}
	if len(images) > maxWeaveImages {
		return nil, status.Errorf(codes.InvalidArgument, "at most %d images per request", maxWeaveImages)
	}
	blocks := []agent.ContentBlock{{Type: "text", Text: req.GetQuery()}}
	for i, img := range images {
		if !weaveImageTypes[img.GetMediaType()] {
			return nil, status.Errorf(codes.InvalidArgument, "images[%d]: media_type must be image/jpeg, image/png, image/gif or image/webp", i)
		}
		if len(img.GetData()) == 0 {
			return nil, status.Errorf(codes.InvalidArgument, "images[%d]: data is empty", i)
		}
		if len(img.GetData()) > maxWeaveImageBytes {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("images[%d]: larger than %d MiB", i, maxWeaveImageBytes>>20))
		}
		blocks = append(blocks, agent.ContentBlock{Type: "image", Image: &types.ImageContent{
			Type: "image",
			Source: types.ImageSource{
				Type:      "base64",
				MediaType: img.GetMediaType(),
				Data:      base64.StdEncoding.EncodeToString(img.GetData()),
			},
		}})
	}
	return blocks, nil
}
