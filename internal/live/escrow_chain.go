package live

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"cappedpoker/poker"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

const escrowMode = "escrow"
const baseSepolia = 84532
const localAnvil = 31337

type escrowSession struct {
	ChainID   uint64           `json:"chainId"`
	Contract  common.Address   `json:"contract"`
	TableID   common.Hash      `json:"tableId"`
	SessionID common.Hash      `json:"sessionId"`
	RulesHash common.Hash      `json:"rulesHash"`
	Roster    []common.Address `json:"roster"`
	BuyInWei  string           `json:"buyInWei"`
}

type escrowSource struct {
	escrowSession
	endpoint string
	client   *http.Client
}

func (s *Server) ConfigureEscrow(ctx context.Context, endpoint, address string) error {
	source, err := loadEscrowSource(ctx, endpoint, address)
	if err != nil {
		return err
	}
	if source.ChainID == baseSepolia && s.store == nil {
		return errors.New("enable PostgreSQL before using Base Sepolia escrow")
	}
	for _, room := range s.rooms {
		match, err := ask(ctx, room, func(state *roomState) int {
			if state.escrow != nil && state.escrow.ChainID == source.ChainID && state.escrow.Contract == source.Contract {
				if state.escrow.TableID != source.TableID || state.escrow.SessionID != source.SessionID ||
					state.escrow.RulesHash != source.RulesHash || state.escrow.BuyInWei != source.BuyInWei ||
					!slices.Equal(state.escrow.Roster, source.Roster) {
					return -1
				}
				state.escrowSource = source
				return 1
			}
			return 0
		})
		if err != nil {
			return err
		}
		if match < 0 {
			return errors.New("configured escrow does not match the saved session")
		}
		s.escrowUsed = s.escrowUsed || match == 1
	}
	s.escrowSource = source
	return nil
}

func loadEscrowSource(ctx context.Context, endpoint, address string) (*escrowSource, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || !common.IsHexAddress(address) {
		return nil, errors.New("configure an escrow RPC URL and contract address")
	}
	source := &escrowSource{
		escrowSession: escrowSession{Contract: common.HexToAddress(address)},
		endpoint:      endpoint,
		client: &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
	raw, err := source.rpc(ctx, "eth_chainId", []any{})
	if err != nil {
		return nil, err
	}
	chain, err := hexutil.DecodeUint64(raw)
	local := u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"
	if err != nil || !((chain == localAnvil && local) || (chain == baseSepolia && u.Scheme == "https")) {
		return nil, errors.New("escrow supports local Anvil or Base Sepolia over HTTPS")
	}
	source.ChainID = chain
	roster, err := source.read(ctx, "players()", "address[]")
	if err != nil {
		return nil, err
	}
	source.Roster = roster.([]common.Address)
	if len(source.Roster) < 2 || len(source.Roster) > 6 {
		return nil, errors.New("escrow must have 2 to 6 players")
	}
	for i, seat := range source.Roster {
		if seat == (common.Address{}) {
			return nil, errors.New("invalid escrow roster")
		}
		for _, previous := range source.Roster[:i] {
			if previous == seat {
				return nil, errors.New("invalid escrow roster")
			}
		}
	}
	for _, field := range []struct {
		method string
		value  *common.Hash
	}{{"tableId()", &source.TableID}, {"sessionId()", &source.SessionID}, {"rulesHash()", &source.RulesHash}} {
		value, err := source.read(ctx, field.method, "bytes32")
		if err != nil {
			return nil, err
		}
		*field.value = value.([32]byte)
	}
	if source.RulesHash != crypto.Keccak256Hash([]byte(poker.RulesVersion)) {
		return nil, errors.New("escrow rules version does not match this poker engine")
	}
	buyIn, err := source.read(ctx, "buyIn()", "uint256")
	if err != nil {
		return nil, err
	}
	amount := buyIn.(*big.Int)
	if amount.Sign() <= 0 || new(big.Int).Mod(amount, big.NewInt(int64(poker.DefaultConfig().BuyIn))).Sign() != 0 {
		return nil, errors.New("escrow buy-in must be a positive multiple of 500 wei")
	}
	source.BuyInWei = amount.String()
	return source, nil
}

func (s *escrowSource) requireActive(ctx context.Context) error {
	value, err := s.read(ctx, "phase()", "uint8")
	if err != nil {
		return err
	}
	if value.(uint8) != 1 {
		return errors.New("escrow must be fully funded and active before dealing")
	}
	return nil
}

func (s *escrowSource) read(ctx context.Context, method, output string) (any, error) {
	raw, err := s.rpc(ctx, "eth_call", []any{map[string]string{
		"to": s.Contract.Hex(), "data": hexutil.Encode(crypto.Keccak256([]byte(method))[:4]),
	}, "latest"})
	if err != nil {
		return nil, err
	}
	data, err := hexutil.Decode(raw)
	if err != nil {
		return nil, errors.New("invalid escrow RPC response")
	}
	typ, err := abi.NewType(output, "", nil)
	if err != nil {
		return nil, err
	}
	values, err := (abi.Arguments{{Type: typ}}).Unpack(data)
	if err != nil || len(values) != 1 {
		return nil, errors.New("configured address did not return escrow metadata")
	}
	return values[0], nil
}

func (s *escrowSource) rpc(ctx context.Context, method string, params []any) (string, error) {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", errors.New("invalid escrow RPC request")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return "", errors.New("could not reach escrow RPC")
	}
	defer response.Body.Close()
	var reply struct {
		Result string          `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 16384)).Decode(&reply) != nil ||
		reply.Result == "" || (len(reply.Error) > 0 && strings.TrimSpace(string(reply.Error)) != "null") {
		return "", errors.New("escrow RPC call failed")
	}
	return reply.Result, nil
}
