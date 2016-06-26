package consensus

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/tendermint/go-common"
	"github.com/tendermint/go-events"
	"github.com/tendermint/go-wire"
	"github.com/tendermint/tendermint/types"
)

/*
	The easiest way to generate this data is to rm ~/.tendermint,
	copy ~/.tendermint_test/somedir/* to ~/.tendermint,
	run `tendermint unsafe_reset_all`,
	set ~/.tendermint/config.toml to use "leveldb" (to create a cswal in data/),
	run `make install`,
	and to run a local node.

	If you need to change the signatures, you can use a script as follows:
	The privBytes comes from config/tendermint_test/...

	```
	package main

	import (
		"encoding/hex"
		"fmt"

		"github.com/tendermint/go-crypto"
	)

	func main() {
		signBytes, err := hex.DecodeString("7B22636861696E5F6964223A2274656E6465726D696E745F74657374222C22766F7465223A7B22626C6F636B5F68617368223A2242453544373939433846353044354645383533364334333932464443384537423342313830373638222C22626C6F636B5F70617274735F686561646572223A506172745365747B543A31204236323237323535464632307D2C22686569676874223A312C22726F756E64223A302C2274797065223A327D7D")
		if err != nil {
			panic(err)
		}
		privBytes, err := hex.DecodeString("27F82582AEFAE7AB151CFB01C48BB6C1A0DA78F9BDDA979A9F70A84D074EB07D3B3069C422E19688B45CBFAE7BB009FC0FA1B1EA86593519318B7214853803C8")
		if err != nil {
			panic(err)
		}
		privKey := crypto.PrivKeyEd25519{}
		copy(privKey[:], privBytes)
		signature := privKey.Sign(signBytes)
		signatureEd25519 := signature.(crypto.SignatureEd25519)
		fmt.Printf("Signature Bytes: %X\n", signatureEd25519[:])
	}
	```
*/

// must not begin with new line
var testLog = `{"time":"2016-08-20T22:06:16.075Z","msg":[3,{"duration":0,"height":1,"round":0,"step":1}]}
{"time":"2016-08-20T22:06:16.077Z","msg":[1,{"height":1,"round":0,"step":"RoundStepPropose"}]}
{"time":"2016-08-20T22:06:16.077Z","msg":[2,{"msg":[17,{"Proposal":{"height":1,"round":0,"block_parts_header":{"total":1,"hash":"BC874710A7514C59E4F460A9621C538CF82C160A"},"pol_round":-1,"pol_block_id":{"hash":"","parts":{"total":0,"hash":""}},"signature":"E5ABDE10A4D3819184850AF85207DFD2DB8A9D3C540C4313C22DFA470AE99CDF43E5511E4FB7AAC040134E27AA8FC42869C655B4D812175ACAC32BAB7E51C906"}}],"peer_key":""}]}
{"time":"2016-08-20T22:06:16.078Z","msg":[2,{"msg":[19,{"Height":1,"Round":0,"Part":{"index":0,"bytes":"0101010F74656E6465726D696E745F746573740101146CA357E0F5D8C00000000000000114C4B01D3810579550997AC5641E759E20D99B51C10001000100","proof":{"aunts":[]}}}],"peer_key":""}]}
{"time":"2016-08-20T22:06:16.079Z","msg":[1,{"height":1,"round":0,"step":"RoundStepPrevote"}]}
{"time":"2016-08-20T22:06:16.079Z","msg":[2,{"msg":[20,{"Vote":{"validator_address":"D028C9981F7A87F3093672BF0D5B0E2A1B3ED456","validator_index":0,"height":1,"round":0,"type":1,"block_id":{"hash":"D98DD4077A50690BAF670E26FD8E56AFC6ACFF4D","parts":{"total":1,"hash":"BC874710A7514C59E4F460A9621C538CF82C160A"}},"signature":"834DE6E3F530178DC695DAE92B2EEF2E31704E58256AF21A27045918A7F225CF59D24B345518C21F67E77559E0953E14DA7372863365242A22BDE2AE1BEABD04"}}],"peer_key":""}]}
{"time":"2016-08-20T22:06:16.080Z","msg":[1,{"height":1,"round":0,"step":"RoundStepPrecommit"}]}
{"time":"2016-08-20T22:06:16.080Z","msg":[2,{"msg":[20,{"Vote":{"validator_address":"D028C9981F7A87F3093672BF0D5B0E2A1B3ED456","validator_index":0,"height":1,"round":0,"type":2,"block_id":{"hash":"D98DD4077A50690BAF670E26FD8E56AFC6ACFF4D","parts":{"total":1,"hash":"BC874710A7514C59E4F460A9621C538CF82C160A"}},"signature":"3028C891510029A00859E4C56E4D0836C9B3E1F5A8F7CFF9E1B36D40E7727A7A64615ACACF1791C453C87E9FBFE66D978566DA92A12C6ABD7307FF5D1430A408"}}],"peer_key":""}]}
`

func init() {
	if strings.HasPrefix(testLog, "\n") {
		panic("testLog should not begin with new line")
	}
}

// map lines in the above wal to privVal step
var mapPrivValStep = map[int]int8{
	0: 0,
	1: 0,
	2: 1,
	3: 1,
	4: 1,
	5: 2,
	6: 2,
	7: 3,
}

func writeWAL(log string) string {
	fmt.Println("writing", log)
	// write the needed wal to file
	f, err := ioutil.TempFile(os.TempDir(), "replay_test_")
	if err != nil {
		panic(err)
	}

	_, err = f.WriteString(log)
	if err != nil {
		panic(err)
	}
	name := f.Name()
	f.Close()
	return name
}

func waitForBlock(newBlockCh chan interface{}) {
	after := time.After(time.Second * 10)
	select {
	case <-newBlockCh:
	case <-after:
		panic("Timed out waiting for new block")
	}
}

func runReplayTest(t *testing.T, cs *ConsensusState, fileName string, newBlockCh chan interface{}) {
	cs.config.Set("cswal", fileName)
	cs.Start()
	// Wait to make a new block.
	// This is just a signal that we haven't halted; its not something contained in the WAL itself.
	// Assuming the consensus state is running, replay of any WAL, including the empty one,
	// should eventually be followed by a new block, or else something is wrong
	waitForBlock(newBlockCh)
	cs.Stop()
}

func toPV(pv PrivValidator) *types.PrivValidator {
	return pv.(*types.PrivValidator)
}

func setupReplayTest(nLines int, crashAfter bool) (*ConsensusState, chan interface{}, string, string) {
	fmt.Println("-------------------------------------")
	log.Notice(Fmt("Starting replay test of %d lines of WAL. Crash after = %v", nLines, crashAfter))

	lineStep := nLines
	if crashAfter {
		lineStep -= 1
	}

	split := strings.Split(testLog, "\n")
	lastMsg := split[nLines]

	// we write those lines up to (not including) one with the signature
	fileName := writeWAL(strings.Join(split[:nLines], "\n") + "\n")

	cs := fixedConsensusState()

	// set the last step according to when we crashed vs the wal
	toPV(cs.privValidator).LastHeight = 1 // first block
	toPV(cs.privValidator).LastStep = mapPrivValStep[lineStep]

	log.Warn("setupReplayTest", "LastStep", toPV(cs.privValidator).LastStep)

	newBlockCh := subscribeToEvent(cs.evsw, "tester", types.EventStringNewBlock(), 1)

	return cs, newBlockCh, lastMsg, fileName
}

//-----------------------------------------------
// Test the log at every iteration, and set the privVal last step
// as if the log was written after signing, before the crash

func TestReplayCrashAfterWrite(t *testing.T) {
	split := strings.Split(testLog, "\n")
	for i := 0; i < len(split)-1; i++ {
		cs, newBlockCh, _, f := setupReplayTest(i+1, true)
		runReplayTest(t, cs, f, newBlockCh)
	}
}

//-----------------------------------------------
// Test the log as if we crashed after signing but before writing.
// This relies on privValidator.LastSignature being set

func TestReplayCrashBeforeWritePropose(t *testing.T) {
	cs, newBlockCh, proposalMsg, f := setupReplayTest(2, false) // propose
	// Set LastSig
	var err error
	var msg ConsensusLogMessage
	wire.ReadJSON(&msg, []byte(proposalMsg), &err)
	proposal := msg.Msg.(msgInfo).Msg.(*ProposalMessage)
	if err != nil {
		t.Fatalf("Error reading json data: %v", err)
	}
	toPV(cs.privValidator).LastSignBytes = types.SignBytes(cs.state.ChainID, proposal.Proposal)
	toPV(cs.privValidator).LastSignature = proposal.Proposal.Signature
	runReplayTest(t, cs, f, newBlockCh)
}

func TestReplayCrashBeforeWritePrevote(t *testing.T) {
	cs, newBlockCh, voteMsg, f := setupReplayTest(5, false) // prevote
	cs.evsw.AddListenerForEvent("tester", types.EventStringCompleteProposal(), func(data events.EventData) {
		// Set LastSig
		var err error
		var msg ConsensusLogMessage
		wire.ReadJSON(&msg, []byte(voteMsg), &err)
		vote := msg.Msg.(msgInfo).Msg.(*VoteMessage)
		if err != nil {
			t.Fatalf("Error reading json data: %v", err)
		}
		toPV(cs.privValidator).LastSignBytes = types.SignBytes(cs.state.ChainID, vote.Vote)
		toPV(cs.privValidator).LastSignature = vote.Vote.Signature
	})
	runReplayTest(t, cs, f, newBlockCh)
}

func TestReplayCrashBeforeWritePrecommit(t *testing.T) {
	cs, newBlockCh, voteMsg, f := setupReplayTest(7, false) // precommit
	cs.evsw.AddListenerForEvent("tester", types.EventStringPolka(), func(data events.EventData) {
		// Set LastSig
		var err error
		var msg ConsensusLogMessage
		wire.ReadJSON(&msg, []byte(voteMsg), &err)
		vote := msg.Msg.(msgInfo).Msg.(*VoteMessage)
		if err != nil {
			t.Fatalf("Error reading json data: %v", err)
		}
		toPV(cs.privValidator).LastSignBytes = types.SignBytes(cs.state.ChainID, vote.Vote)
		toPV(cs.privValidator).LastSignature = vote.Vote.Signature
	})
	runReplayTest(t, cs, f, newBlockCh)
}
