// SPDX-License-Identifier: MIT
pragma solidity 0.8.24;

import {ECDSA} from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";
import {EIP712} from "@openzeppelin/contracts/utils/cryptography/EIP712.sol";

/// @notice Experimental native ETH escrow for one fixed poker session.
contract PokerEscrow is EIP712 {
    enum Phase {
        Funding,
        Active,
        Challenge,
        Refundable,
        Withdrawable
    }

    struct Checkpoint {
        uint256 sequence;
        uint256[] balances;
        bytes32 transcriptHash;
    }

    bytes32 private constant CHECKPOINT_TYPEHASH = keccak256(
        "Checkpoint(bytes32 tableId,bytes32 sessionId,bytes32 rulesHash,uint256 sequence,uint256[] balances,bytes32 transcriptHash)"
    );

    bytes32 public immutable tableId;
    bytes32 public immutable sessionId;
    bytes32 public immutable rulesHash;
    uint256 public immutable buyIn;
    uint256 public immutable fundingDeadline;
    uint64 public immutable challengeDuration;

    address[] private roster;
    mapping(address => uint256) private seatPlusOne;
    mapping(address => uint256) public balanceOf;
    uint256 public fundedCount;
    uint256 public exitDeadline;
    uint256 public sequence;

    error InvalidSession();
    error InvalidRoster();
    error WrongPhase();
    error NotPlayer();
    error WrongDeposit();
    error AlreadyFunded();
    error InvalidCheckpoint();
    error StaleCheckpoint();
    error InvalidSigner(uint256 seat);
    error InvalidRecipient();
    error NothingToWithdraw();
    error TransferFailed();

    event Deposited(address indexed player, uint256 amount);
    event Activated();
    event ExitStarted(address indexed player, uint256 deadline);
    event CheckpointAccepted(uint256 indexed sequence, bytes32 indexed digest, bytes32 transcriptHash);
    event Withdrawn(address indexed player, address indexed recipient, uint256 amount);

    constructor(
        bytes32 tableId_,
        bytes32 sessionId_,
        bytes32 rulesHash_,
        address[] memory players_,
        uint256 buyIn_,
        uint64 fundingDuration_,
        uint64 challengeDuration_
    ) EIP712("Poker Lab Escrow", "1") {
        if (players_.length < 2 || players_.length > 6) revert InvalidRoster();
        if (
            tableId_ == bytes32(0) || sessionId_ == bytes32(0) || rulesHash_ == bytes32(0) || buyIn_ == 0
                || buyIn_ > type(uint256).max / players_.length || fundingDuration_ == 0 || challengeDuration_ == 0
        ) revert InvalidSession();

        for (uint256 i = 0; i < players_.length; ++i) {
            address player = players_[i];
            if (player == address(0) || seatPlusOne[player] != 0) revert InvalidRoster();
            seatPlusOne[player] = i + 1;
            roster.push(player);
        }
        tableId = tableId_;
        sessionId = sessionId_;
        rulesHash = rulesHash_;
        buyIn = buyIn_;
        fundingDeadline = block.timestamp + fundingDuration_;
        challengeDuration = challengeDuration_;
    }

    function players() external view returns (address[] memory) {
        return roster;
    }

    function phase() public view returns (Phase) {
        if (fundedCount < roster.length) {
            return block.timestamp < fundingDeadline ? Phase.Funding : Phase.Refundable;
        }
        if (exitDeadline == 0) return Phase.Active;
        return block.timestamp < exitDeadline ? Phase.Challenge : Phase.Withdrawable;
    }

    function deposit() external payable {
        if (phase() != Phase.Funding) revert WrongPhase();
        _requirePlayer(msg.sender);
        if (msg.value != buyIn) revert WrongDeposit();
        if (balanceOf[msg.sender] != 0) revert AlreadyFunded();
        balanceOf[msg.sender] = msg.value;
        ++fundedCount;
        emit Deposited(msg.sender, msg.value);
        if (fundedCount == roster.length) emit Activated();
    }

    /// @notice Begin exit from the original deposits, with time to post a signed state.
    function startExit() external {
        _startExit();
    }

    /// @notice Begin exit and post an agreed checkpoint in the same transaction.
    function startExitWithCheckpoint(Checkpoint calldata checkpoint, bytes[] calldata signatures) external {
        _startExit();
        _acceptCheckpoint(checkpoint, signatures);
    }

    /// @notice Anyone holding a newer fully signed checkpoint can relay it during exit.
    function challenge(Checkpoint calldata checkpoint, bytes[] calldata signatures) external {
        if (phase() != Phase.Challenge) revert WrongPhase();
        _acceptCheckpoint(checkpoint, signatures);
    }

    function checkpointDigest(Checkpoint calldata checkpoint) public view returns (bytes32) {
        return _hashTypedDataV4(
            keccak256(
                abi.encode(
                    CHECKPOINT_TYPEHASH,
                    tableId,
                    sessionId,
                    rulesHash,
                    checkpoint.sequence,
                    keccak256(abi.encodePacked(checkpoint.balances)),
                    checkpoint.transcriptHash
                )
            )
        );
    }

    function withdraw(address payable recipient) external {
        Phase current = phase();
        if (current != Phase.Refundable && current != Phase.Withdrawable) revert WrongPhase();
        _requirePlayer(msg.sender);
        if (recipient == address(0)) revert InvalidRecipient();
        uint256 amount = balanceOf[msg.sender];
        if (amount == 0) revert NothingToWithdraw();
        balanceOf[msg.sender] = 0;
        (bool sent,) = recipient.call{value: amount}("");
        if (!sent) revert TransferFailed();
        emit Withdrawn(msg.sender, recipient, amount);
    }

    function _startExit() private {
        _requirePlayer(msg.sender);
        if (phase() != Phase.Active) revert WrongPhase();
        exitDeadline = block.timestamp + challengeDuration;
        emit ExitStarted(msg.sender, exitDeadline);
    }

    function _acceptCheckpoint(Checkpoint calldata checkpoint, bytes[] calldata signatures) private {
        uint256 count = roster.length;
        if (checkpoint.sequence <= sequence) revert StaleCheckpoint();
        if (
            checkpoint.balances.length != count || signatures.length != count || checkpoint.transcriptHash == bytes32(0)
        ) {
            revert InvalidCheckpoint();
        }

        uint256 remaining = buyIn * count;
        for (uint256 i = 0; i < count; ++i) {
            uint256 amount = checkpoint.balances[i];
            if (amount > remaining) revert InvalidCheckpoint();
            remaining -= amount;
        }
        if (remaining != 0) revert InvalidCheckpoint();

        bytes32 digest = checkpointDigest(checkpoint);
        for (uint256 i = 0; i < count; ++i) {
            address player = roster[i];
            if (ECDSA.recover(digest, signatures[i]) != player) revert InvalidSigner(i);
            balanceOf[player] = checkpoint.balances[i];
        }
        sequence = checkpoint.sequence;
        emit CheckpointAccepted(sequence, digest, checkpoint.transcriptHash);
    }

    function _requirePlayer(address player) private view {
        if (seatPlusOne[player] == 0) revert NotPlayer();
    }
}
