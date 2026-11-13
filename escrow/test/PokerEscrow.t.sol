// SPDX-License-Identifier: MIT
pragma solidity 0.8.24;

import {PokerEscrow} from "../src/PokerEscrow.sol";

interface Vm {
    function addr(uint256 privateKey) external returns (address);
    function chainId(uint256 newChainId) external;
    function deal(address who, uint256 balance) external;
    function expectRevert(bytes4 selector) external;
    function expectPartialRevert(bytes4 selector) external;
    function prank(address sender) external;
    function sign(uint256 privateKey, bytes32 digest) external returns (uint8 v, bytes32 r, bytes32 s);
    function warp(uint256 timestamp) external;
}

contract RejectEther {
    receive() external payable {
        revert();
    }
}

contract PokerEscrowTest {
    Vm private constant vm = Vm(address(uint160(uint256(keccak256("hevm cheat code")))));
    uint256 private constant BUY_IN = 1 ether;
    uint64 private constant FUNDING = 10 minutes;
    uint64 private constant CHALLENGE = 5 minutes;

    PokerEscrow private escrow;
    address private alice;
    address private bob;
    address private constant RELAYER = address(0xCAFE);

    function setUp() public {
        alice = vm.addr(1);
        bob = vm.addr(2);
        vm.deal(alice, 10 ether);
        vm.deal(bob, 10 ether);
        escrow = _newEscrow();
    }

    function testFundingRequiresExactDepositFromEverySeat() public {
        vm.prank(RELAYER);
        vm.expectRevert(PokerEscrow.NotPlayer.selector);
        escrow.deposit();

        vm.prank(alice);
        vm.expectRevert(PokerEscrow.WrongDeposit.selector);
        escrow.deposit{value: BUY_IN - 1}();

        _deposit(escrow, alice);
        vm.prank(alice);
        vm.expectRevert(PokerEscrow.AlreadyFunded.selector);
        escrow.deposit{value: BUY_IN}();

        require(escrow.phase() == PokerEscrow.Phase.Funding);
        _deposit(escrow, bob);
        require(escrow.phase() == PokerEscrow.Phase.Active);
        require(address(escrow).balance == 2 * BUY_IN);
    }

    function testIncompleteFundingRefundsAfterDeadline() public {
        _deposit(escrow, alice);
        vm.prank(alice);
        vm.expectRevert(PokerEscrow.WrongPhase.selector);
        escrow.withdraw(payable(alice));

        vm.warp(escrow.fundingDeadline());
        require(escrow.phase() == PokerEscrow.Phase.Refundable);
        vm.prank(bob);
        vm.expectRevert(PokerEscrow.WrongPhase.selector);
        escrow.deposit{value: BUY_IN}();
        _withdraw(escrow, alice, BUY_IN);
        require(address(escrow).balance == 0);
    }

    function testMissingSignatureFallsBackToDeposits() public {
        _fund(escrow);
        PokerEscrow.Checkpoint memory unsignedHand = _checkpoint(1, 1.5 ether);
        bytes[] memory incomplete = new bytes[](1);
        incomplete[0] = _signature(1, escrow.checkpointDigest(unsignedHand));
        vm.prank(alice);
        vm.expectRevert(PokerEscrow.InvalidCheckpoint.selector);
        escrow.startExitWithCheckpoint(unsignedHand, incomplete);
        require(escrow.phase() == PokerEscrow.Phase.Active);

        vm.prank(alice);
        escrow.startExit();
        require(escrow.sequence() == 0);
        vm.warp(escrow.exitDeadline());
        _withdraw(escrow, alice, BUY_IN);
        _withdraw(escrow, bob, BUY_IN);
        require(address(escrow).balance == 0);
    }

    function testNewerCheckpointBeatsStaleExitWithoutExtendingDeadline() public {
        _fund(escrow);
        PokerEscrow.Checkpoint memory stale = _checkpoint(1, 0.5 ether);
        PokerEscrow.Checkpoint memory latest = _checkpoint(2, 1.5 ether);
        bytes[] memory oldSignatures = _sign(escrow, stale);
        bytes[] memory newSignatures = _sign(escrow, latest);
        vm.prank(bob);
        escrow.startExitWithCheckpoint(stale, oldSignatures);
        uint256 deadline = escrow.exitDeadline();

        vm.warp(deadline - 1);
        vm.prank(RELAYER);
        escrow.challenge(latest, newSignatures);
        require(escrow.sequence() == 2 && escrow.exitDeadline() == deadline);
        vm.expectRevert(PokerEscrow.StaleCheckpoint.selector);
        escrow.challenge(stale, oldSignatures);

        vm.prank(alice);
        vm.expectRevert(PokerEscrow.WrongPhase.selector);
        escrow.withdraw(payable(alice));
        vm.warp(deadline);
        vm.expectRevert(PokerEscrow.WrongPhase.selector);
        escrow.challenge(latest, newSignatures);

        _withdraw(escrow, alice, 1.5 ether);
        _withdraw(escrow, bob, 0.5 ether);
        require(address(escrow).balance == 0);
    }

    function testUnsignedLaterHandKeepsLastAgreedCheckpoint() public {
        _fund(escrow);
        PokerEscrow.Checkpoint memory agreed = _checkpoint(7, 1.25 ether);
        bytes[] memory signatures = _sign(escrow, agreed);
        vm.prank(bob);
        escrow.startExitWithCheckpoint(agreed, signatures);
        PokerEscrow.Checkpoint memory unsignedHand = _checkpoint(8, 0.25 ether);
        bytes[] memory incomplete = new bytes[](1);
        incomplete[0] = _signature(1, escrow.checkpointDigest(unsignedHand));
        vm.expectRevert(PokerEscrow.InvalidCheckpoint.selector);
        escrow.challenge(unsignedHand, incomplete);

        vm.warp(escrow.exitDeadline());
        _withdraw(escrow, alice, 1.25 ether);
        _withdraw(escrow, bob, 0.75 ether);
    }

    function testRejectsIncorrectTotalAndTamperedTranscript() public {
        _fund(escrow);
        PokerEscrow.Checkpoint memory checkpoint = _checkpoint(1, BUY_IN);
        bytes[] memory signatures = _sign(escrow, checkpoint);
        checkpoint.balances[1] += 1;
        vm.prank(alice);
        vm.expectRevert(PokerEscrow.InvalidCheckpoint.selector);
        escrow.startExitWithCheckpoint(checkpoint, signatures);

        checkpoint.balances[1] -= 1;
        checkpoint.transcriptHash = keccak256("different transcript");
        vm.prank(alice);
        vm.expectPartialRevert(PokerEscrow.InvalidSigner.selector);
        escrow.startExitWithCheckpoint(checkpoint, signatures);
        require(escrow.phase() == PokerEscrow.Phase.Active);
    }

    function testSignaturesCannotBeReplayedOnAnotherContractOrChain() public {
        _fund(escrow);
        PokerEscrow.Checkpoint memory checkpoint = _checkpoint(1, BUY_IN);
        bytes[] memory signatures = _sign(escrow, checkpoint);
        PokerEscrow other = _newEscrow();
        _fund(other);
        vm.prank(alice);
        vm.expectPartialRevert(PokerEscrow.InvalidSigner.selector);
        other.startExitWithCheckpoint(checkpoint, signatures);

        vm.chainId(block.chainid + 1);
        vm.prank(alice);
        vm.expectPartialRevert(PokerEscrow.InvalidSigner.selector);
        escrow.startExitWithCheckpoint(checkpoint, signatures);
    }

    function testWithdrawalCanBeRetriedAfterTransferFailureAndPreventsDoubleClaim() public {
        _fund(escrow);
        vm.prank(alice);
        escrow.startExit();
        vm.warp(escrow.exitDeadline());
        RejectEther reject = new RejectEther();
        vm.prank(alice);
        vm.expectRevert(PokerEscrow.TransferFailed.selector);
        escrow.withdraw(payable(address(reject)));
        _withdraw(escrow, alice, BUY_IN);

        vm.prank(alice);
        vm.expectRevert(PokerEscrow.NothingToWithdraw.selector);
        escrow.withdraw(payable(alice));
        _withdraw(escrow, bob, BUY_IN);
    }

    function testFuzzFullySignedBalancesConserveEscrow(uint256 amount) public {
        _fund(escrow);
        uint256 aliceBalance = amount % (2 * BUY_IN + 1);
        PokerEscrow.Checkpoint memory checkpoint = _checkpoint(1, aliceBalance);
        bytes[] memory signatures = _sign(escrow, checkpoint);
        vm.prank(alice);
        escrow.startExitWithCheckpoint(checkpoint, signatures);
        vm.warp(escrow.exitDeadline());
        if (aliceBalance > 0) _withdraw(escrow, alice, aliceBalance);
        uint256 bobBalance = 2 * BUY_IN - aliceBalance;
        if (bobBalance > 0) _withdraw(escrow, bob, bobBalance);
        require(address(escrow).balance == 0);
    }

    function _newEscrow() private returns (PokerEscrow) {
        address[] memory players = new address[](2);
        players[0] = alice;
        players[1] = bob;
        return new PokerEscrow(
            keccak256("table"), keccak256("session"), keccak256("capped-holdem-v1"), players, BUY_IN, FUNDING, CHALLENGE
        );
    }

    function _deposit(PokerEscrow target, address player) private {
        vm.prank(player);
        target.deposit{value: BUY_IN}();
    }

    function _fund(PokerEscrow target) private {
        _deposit(target, alice);
        _deposit(target, bob);
    }

    function _checkpoint(uint256 sequence, uint256 aliceBalance) private pure returns (PokerEscrow.Checkpoint memory) {
        uint256[] memory balances = new uint256[](2);
        balances[0] = aliceBalance;
        balances[1] = 2 * BUY_IN - aliceBalance;
        return PokerEscrow.Checkpoint(sequence, balances, keccak256(abi.encode("hand", sequence)));
    }

    function _sign(PokerEscrow target, PokerEscrow.Checkpoint memory checkpoint) private returns (bytes[] memory) {
        bytes32 digest = target.checkpointDigest(checkpoint);
        bytes[] memory signatures = new bytes[](2);
        signatures[0] = _signature(1, digest);
        signatures[1] = _signature(2, digest);
        return signatures;
    }

    function _signature(uint256 privateKey, bytes32 digest) private returns (bytes memory) {
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(privateKey, digest);
        return abi.encodePacked(r, s, v);
    }

    function _withdraw(PokerEscrow target, address player, uint256 expected) private {
        uint256 before = player.balance;
        vm.prank(player);
        target.withdraw(payable(player));
        require(player.balance - before == expected);
    }
}
