/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/golang/protobuf/proto"
	"github.com/hyperledger-labs/weaver-dlt-interoperability/common/protos-go/common"
	fabric2 "github.com/hyperledger-labs/weaver-dlt-interoperability/common/protos-go/fabric"
	"github.com/hyperledger-labs/weaver-dlt-interoperability/sdks/fabric/go-sdk/interoperablehelper"
	"github.com/hyperledger-labs/weaver-dlt-interoperability/sdks/fabric/go-sdk/types"
	"github.com/hyperledger/fabric-protos-go/msp"
	"github.com/hyperledger/fabric/protoutil"
	"github.com/pkg/errors"

	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
)

const (
	// OK constant - status code less than 400, endorser will endorse it.
	// OK means init or invoke successfully.
	OK = 200
)

// Result models the result of a query to a remote network using Relay
type Result struct {
	address string
	view    *common.View
	fv      *fabric2.FabricView
	results []byte
}

func NewResult(address string, view *common.View) (*Result, error) {
	if view.Meta.Protocol != common.Meta_FABRIC {
		return nil, errors.Errorf("invalid protocol, expected Meta_FABRIC, got [%d]", view.Meta.Protocol)
	}
	if view.Meta.SerializationFormat != "Protobuf" {
		return nil, errors.Errorf("invalid serialization format, expected Protobuf, got [%s]", view.Meta.SerializationFormat)
	}

	fv := &fabric2.FabricView{}
	if err := proto.Unmarshal(view.Data, fv); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling view's data")
	}

	// TODO: check consistency with the requested query
	respPayload, err := protoutil.UnmarshalChaincodeAction(fv.ProposalResponsePayload.Extension)
	if err != nil {
		err = fmt.Errorf("GetChaincodeAction error %s", err)
		return nil, err
	}

	return &Result{
		address: address,
		view:    view,
		fv:      fv,
		results: respPayload.Results,
	}, nil
}

// IsOK return true if the result is valid
func (r *Result) IsOK() bool {
	return r.fv.Response.Status == OK
}

// Results return the marshalled version of the Fabric rwset
func (r *Result) Results() []byte {
	return r.results
}

// RWSet returns a wrapper over the Fabric rwset to inspect it
func (r *Result) RWSet() (*Inspector, error) {
	i := newInspector()
	if err := i.rws.populate(r.results, "ephemeral"); err != nil {
		return nil, err
	}
	return i, nil
}

// Proof returns the marshalled version of the proof of validity accompanying this result
func (r *Result) Proof() ([]byte, error) {
	viewRaw, err := proto.Marshal(r.view)
	if err != nil {
		return nil, errors.Wrapf(err, "failed marshalling view")
	}
	return json.Marshal(&Proof{
		B64ViewProto: base64.StdEncoding.EncodeToString(viewRaw),
		Address:      r.address,
	})
}

// Query models a query to a remote network using Relay
type Query struct {
	localFNS       *fabric.NetworkService
	remoteID       *ID
	remoteFunction string
	remoteArgs     []interface{}
}

func NewQuery(fns *fabric.NetworkService, remoteID *ID, function string, args []interface{}) *Query {
	return &Query{
		localFNS:       fns,
		remoteID:       remoteID,
		remoteFunction: function,
		remoteArgs:     args,
	}
}

// Call performs the query and return a result if no error occurred
func (q *Query) Call() (*Result, error) {
	localRelayAddress := q.localFNS.ConfigService().GetString("weaver.relay.address")

	remoteRelayAddress := q.localFNS.ConfigService().GetString(fmt.Sprintf("weaver.remote.%s.address" + q.remoteID.Network))

	args, err := q.prepareArgs()
	if err != nil {
		return nil, errors.WithMessagef(err, "failed parsing arguments")
	}
	invokeObject := types.Query{
		Channel:      q.remoteID.Channel,
		ContractName: q.remoteID.Chaincode,
		CcFunc:       q.remoteFunction,
		CcArgs:       args,
	}
	specialAddress := createAddress(
		invokeObject,
		q.remoteID.Network,
		remoteRelayAddress,
	)
	interopJSON := types.InteropJSON{
		Address:        specialAddress,
		RemoteEndPoint: remoteRelayAddress,
		Sign:           true,
		NetworkId:      q.remoteID.Network,
		ChannelId:      q.remoteID.Channel,
		ChaincodeId:    q.remoteID.Chaincode,
		ChaincodeFunc:  q.remoteFunction,
		CcArgs:         args,
	}

	me := q.localFNS.IdentityProvider().DefaultIdentity()
	sigSvc := q.localFNS.SigService()
	signer, err := sigSvc.GetSigner(me)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting signer for default identity [%s]", me)
	}

	// TODO: replace with a higher level function
	sID := &msp.SerializedIdentity{}
	if err := proto.Unmarshal(me, sID); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling fabric identity")
	}

	views, _, err := interoperablehelper.InteropFlow(
		&contract{
			fns:       q.localFNS,
			channel:   q.localFNS.ConfigService().GetString("weaver.interopcc.channel"),
			namespace: q.localFNS.ConfigService().GetString("weaver.interopcc.name"),
		},
		q.localFNS.Name(),
		invokeObject,
		sID.Mspid,
		localRelayAddress,
		[]int{1},
		[]types.InteropJSON{interopJSON},
		signer,
		string(sID.IdBytes),
		true,
	)
	if err != nil {
		return nil, errors.Wrapf(err, "failed running interop view")
	}

	return NewResult(specialAddress, views[0])
}

func (q *Query) prepareArgs() ([]string, error) {
	var args []string
	for _, arg := range q.remoteArgs {
		b, err := q.argToString(arg)
		if err != nil {
			return nil, err
		}
		args = append(args, b)
	}
	return args, nil
}

func (q *Query) argToString(arg interface{}) (string, error) {
	switch v := arg.(type) {
	case []byte:
		return string(v), nil
	case string:
		return v, nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	default:
		return "", errors.Errorf("arg type [%T] not recognized.", v)
	}
}

func createAddress(query types.Query, networkId, remoteURL string) string {
	addressString := remoteURL + "/" + networkId + "/" + query.Channel + ":" + query.ContractName + ":" + query.CcFunc + ":" + query.CcArgs[0]
	return addressString
}

type contract struct {
	fns       *fabric.NetworkService
	channel   string
	namespace string
}

func (f contract) transact(functionName string, args ...string) ([]byte, error) {
	var chaincodeArgs []interface{}
	for _, arg := range args {
		chaincodeArgs = append(chaincodeArgs, arg)
	}

	channel, err := f.fns.Channel(f.channel)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting channel [%s:%s]", f.fns.Name(), f.channel)
	}
	res, err := channel.Chaincode(f.namespace).Query(
		functionName, chaincodeArgs...,
	).WithInvokerIdentity(
		f.fns.IdentityProvider().DefaultIdentity(),
	).Call()
	if err != nil {
		return nil, errors.WithMessagef(err, "failed invoking interop chaincode [%s.%s.%s:%s]", f.fns.Name(), f.channel, f.namespace, functionName)
	}

	return res.([]byte), nil
}

func (f contract) EvaluateTransaction(name string, args ...string) ([]byte, error) {
	return f.transact(name, args...)
}

func (f contract) SubmitTransaction(name string, args ...string) ([]byte, error) {
	panic("we shouldn't use this")
}
