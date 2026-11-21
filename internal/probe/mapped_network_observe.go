package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/omry/reploy/internal/canonical"
)

// MappedNetworkObservationV1 is private host-observer input, not an
// application assertion or blueprint authority. The trusted caller pins the
// namespace and reads resolvers through the held target root.
type MappedNetworkObservationV1 struct {
	Request                  canonical.Digest
	Namespace                string
	Arguments, Resolvers     []string
	Public, Local, Ambiguous bool
}

func VerifyMappedNetworkPolicyV1(ctx context.Context, input MappedNetworkObservationV1) error {
	if ctx == nil || ctx.Err() != nil {
		return fmt.Errorf("mapped network observation requires a live context")
	}
	return verifyMappedNetworkPolicyV1(ctx, input)
}

func mappedNetworkObservationMainV1(stdin io.Reader, stdout io.Writer) error {
	data, err := io.ReadAll(io.LimitReader(stdin, (512<<10)+1))
	if err != nil || len(data) > 512<<10 {
		return fmt.Errorf("read bounded mapped observation: %v", err)
	}
	var input MappedNetworkObservationV1
	if err := json.Unmarshal(data, &input); err != nil {
		return err
	}
	encoded, err := canonical.Marshal(input)
	if err != nil || !bytes.Equal(encoded, data) || input.Request.Validate() != nil {
		return fmt.Errorf("mapped observation requires its exact canonical bound request")
	}
	namespace, err := os.Readlink("/proc/self/ns/net")
	if err != nil || namespace != input.Namespace {
		return fmt.Errorf("mapped observation is in a different network namespace: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := VerifyMappedNetworkPolicyV1(ctx, input); err != nil {
		return err
	}
	result, err := canonical.Marshal(struct {
		Request   canonical.Digest
		Namespace string
	}{input.Request, namespace})
	if err == nil {
		_, err = stdout.Write(result)
	}
	return err
}
