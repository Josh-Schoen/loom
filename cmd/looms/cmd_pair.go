// Copyright 2026 Teradata
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"rsc.io/qr"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	loomconfig "github.com/teradata-labs/loom/pkg/config"
	"github.com/teradata-labs/loom/pkg/devices"
	"github.com/teradata-labs/loom/pkg/household"
)

const (
	devicesFileName          = "devices.json"
	deviceAdminTokenFileName = "device-admin.token"
)

var (
	pairServer   string
	pairUser     string
	pairName     string
	pairTTL      time.Duration
	pairLinkOnly bool
	devicesJSON  bool
)

var pairCmd = &cobra.Command{
	Use:   "pair",
	Short: "Show a QR code that connects a phone or app to this server",
	Long: `Issues a single-use pairing code on the running server and prints it as a
QR code and link. Scan the QR code with the phone (or open the link on it) to
connect the app without typing an address or key.

Requires server.device_pairing.enabled: true. Run on the machine that runs
'looms serve', as the same user: the command reads the server's admin token
from the data directory.`,
	RunE: runPair,
}

var devicesCmd = &cobra.Command{
	Use:   "devices",
	Short: "List or revoke paired devices",
}

var devicesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List paired devices",
	RunE:  runDevicesList,
}

var devicesRevokeCmd = &cobra.Command{
	Use:   "revoke DEVICE_ID",
	Short: "Disconnect a paired device permanently",
	Args:  cobra.ExactArgs(1),
	RunE:  runDevicesRevoke,
}

func init() {
	rootCmd.AddCommand(pairCmd)
	rootCmd.AddCommand(devicesCmd)
	devicesCmd.AddCommand(devicesListCmd)
	devicesCmd.AddCommand(devicesRevokeCmd)

	for _, cmd := range []*cobra.Command{pairCmd, devicesListCmd, devicesRevokeCmd} {
		cmd.Flags().StringVar(&pairServer, "server", "localhost:60051", "Loom gRPC server address (plaintext)")
	}
	pairCmd.Flags().StringVar(&pairUser, "user", "", "User the device acts as (default: the server's default user)")
	pairCmd.Flags().StringVar(&pairName, "name", "", "Name of the person the device belongs to (\"Sam\"), kept in people.json for the household; needs --user")
	pairCmd.Flags().DurationVar(&pairTTL, "ttl", 0, "How long the code works (default and maximum: server.device_pairing.code_ttl_seconds)")
	pairCmd.Flags().BoolVar(&pairLinkOnly, "link-only", false, "Print only the pairing link (for scripts)")
	devicesListCmd.Flags().BoolVar(&devicesJSON, "json", false, "Print the devices as JSON (loom.v1.ListDevicesResponse), for scripts and apps")
}

// deviceClient connects to the DeviceService with the admin token attached.
func deviceClient(ctx context.Context) (loomv1.DeviceServiceClient, context.Context, func(), error) {
	tokenPath := filepath.Join(loomconfig.GetLoomDataDir(), deviceAdminTokenFileName)
	token, err := devices.ReadAdminToken(tokenPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil, fmt.Errorf("no admin token at %s: start the server with server.device_pairing.enabled: true first", tokenPath)
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read admin token: %w", err)
	}
	conn, err := grpc.NewClient(pairServer, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connect to %s: %w", pairServer, err)
	}
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	return loomv1.NewDeviceServiceClient(conn), ctx, func() { _ = conn.Close() }, nil
}

// explainDeviceError turns common RPC failures into next steps.
func explainDeviceError(err error) error {
	switch status.Code(err) {
	case codes.Unimplemented:
		return errors.New("device pairing is off on this server: set server.device_pairing.enabled: true and restart it")
	case codes.Unavailable:
		return fmt.Errorf("cannot reach the server at %s: is `looms serve` running?", pairServer)
	case codes.PermissionDenied:
		return errors.New("the server rejected the admin token: run this as the user that runs `looms serve`, with the same LOOM_DATA_DIR")
	}
	return err
}

func runPair(cmd *cobra.Command, _ []string) error {
	if pairName != "" {
		if pairUser == "" {
			return errors.New("--name needs --user: the name is for that user")
		}
		people := household.NewPeople(filepath.Join(loomconfig.GetLoomDataDir(), household.PeopleFileName))
		if err := people.SetName(pairUser, pairName); err != nil {
			return fmt.Errorf("save the name: %w", err)
		}
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
	defer cancel()
	client, ctx, closeConn, err := deviceClient(ctx)
	if err != nil {
		return err
	}
	defer closeConn()

	resp, err := client.CreatePairingCode(ctx, &loomv1.CreatePairingCodeRequest{
		UserId:     pairUser,
		TtlSeconds: int32(pairTTL / time.Second),
	})
	if err != nil {
		return explainDeviceError(err)
	}
	printPairing(cmd.OutOrStdout(), resp, pairLinkOnly)
	return nil
}

func printPairing(w io.Writer, resp *loomv1.CreatePairingCodeResponse, linkOnly bool) {
	if linkOnly {
		_, _ = fmt.Fprintln(w, resp.GetPairingLink())
		return
	}
	_, _ = fmt.Fprintln(w, "Scan this with the phone's camera to connect it:")
	_, _ = fmt.Fprintln(w)
	if err := writeQR(w, resp.GetPairingLink()); err != nil {
		_, _ = fmt.Fprintf(w, "(could not draw the QR code: %v)\n", err)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "Or open this link on the phone:\n  %s\n\n", resp.GetPairingLink())
	_, _ = fmt.Fprintf(w, "The phone will connect to %s.\n", resp.GetServerUrl())
	if strings.HasPrefix(resp.GetServerUrl(), "http://") {
		_, _ = fmt.Fprintln(w, "That address works only on this network, without encryption. To connect from anywhere,")
		_, _ = fmt.Fprintln(w, "set server.device_pairing.public_url to an https address (see docs/architecture/device-pairing.md).")
	}
	_, _ = fmt.Fprintf(w, "The code works once and expires at %s.\n",
		resp.GetExpiresAt().AsTime().Local().Format("3:04 PM MST"))
}

func runDevicesList(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
	defer cancel()
	client, ctx, closeConn, err := deviceClient(ctx)
	if err != nil {
		return err
	}
	defer closeConn()

	resp, err := client.ListDevices(ctx, &loomv1.ListDevicesRequest{})
	if err != nil {
		return explainDeviceError(err)
	}
	if devicesJSON {
		return printDevicesJSON(cmd.OutOrStdout(), resp)
	}
	printDevices(cmd.OutOrStdout(), resp.GetDevices())
	return nil
}

// printDevicesJSON writes resp as protojson (lowerCamelCase fields, as on the
// HTTP gateway), so other programs can read the list without parsing the table.
func printDevicesJSON(w io.Writer, resp *loomv1.ListDevicesResponse) error {
	data, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

func printDevices(w io.Writer, list []*loomv1.Device) {
	if len(list) == 0 {
		_, _ = fmt.Fprintln(w, "No paired devices. Run `looms pair` to connect one.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tNAME\tPLATFORM\tUSER\tPAIRED\tLAST SEEN\tSTATUS")
	for _, d := range list {
		state := "active"
		if d.GetRevokedAt() != nil {
			state = "revoked"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			d.GetId(), d.GetName(), d.GetPlatform(), d.GetUserId(),
			d.GetCreatedAt().AsTime().Local().Format("2006-01-02 15:04"),
			d.GetLastSeenAt().AsTime().Local().Format("2006-01-02 15:04"),
			state)
	}
	_ = tw.Flush()
}

func runDevicesRevoke(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
	defer cancel()
	client, ctx, closeConn, err := deviceClient(ctx)
	if err != nil {
		return err
	}
	defer closeConn()

	resp, err := client.RevokeDevice(ctx, &loomv1.RevokeDeviceRequest{DeviceId: args[0]})
	if err != nil {
		return explainDeviceError(err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Revoked %s (%s). It can no longer connect.\n",
		resp.GetDevice().GetId(), resp.GetDevice().GetName())
	return nil
}

// qrQuietZone is the light border, in modules, that scanners need around a code.
const qrQuietZone = 2

// writeQR draws text as a QR code using half-block characters, two modules per
// character cell. Colors are set explicitly (white on black) so the code has
// the right polarity on light and dark terminal themes alike.
func writeQR(w io.Writer, text string) error {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return err
	}
	dark := func(x, y int) bool {
		if x < 0 || y < 0 || x >= code.Size || y >= code.Size {
			return false
		}
		return code.Black(x, y)
	}
	// Foreground draws the upper module ("▀"), background the lower one.
	color := func(isDark bool, base int) int {
		if isDark {
			return base // black
		}
		return base + 7 // white
	}
	var b strings.Builder
	for y := -qrQuietZone; y < code.Size+qrQuietZone; y += 2 {
		for x := -qrQuietZone; x < code.Size+qrQuietZone; x++ {
			fmt.Fprintf(&b, "\x1b[%d;%dm▀", color(dark(x, y), 30), color(dark(x, y+1), 40))
		}
		b.WriteString("\x1b[0m\n")
	}
	_, err = io.WriteString(w, b.String())
	return err
}
