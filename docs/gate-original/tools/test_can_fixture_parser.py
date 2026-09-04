from __future__ import annotations

from pathlib import Path
import sys
import unittest


TOOLS_DIR = Path(__file__).resolve().parent
REPO_ROOT = TOOLS_DIR.parents[2]
sys.path.insert(0, str(TOOLS_DIR))

from can_fixture_parser import (  # noqa: E402
    CanFrame,
    extract_transactions,
    parse_candump,
    parse_candump_file,
    validate_transaction,
)


ARTIFACTS = REPO_ROOT / "artifacts" / "gate-original"


class CanFixtureParserTests(unittest.TestCase):
    def test_valid_captures_have_the_observed_envelope(self) -> None:
        expected_files = {
            "can-scan-20260823.log": 2,
            "can-scan-next-20260823.log": 1,
            "joint-can-20260823.log": 1,
        }

        for file_name, expected_transaction_count in expected_files.items():
            with self.subTest(file_name=file_name):
                frames = parse_candump_file(ARTIFACTS / file_name)
                transactions = extract_transactions(frames)
                self.assertEqual(len(transactions), expected_transaction_count)
                self.assertEqual(
                    [frame.can_id for frame in frames],
                    [0x122, 0x123, 0x122, 0x123] * expected_transaction_count,
                )
                self.assertTrue(all(tx.request_header.dlc == 8 for tx in transactions))
                self.assertTrue(all(tx.response_header.dlc == 8 for tx in transactions))
                self.assertTrue(all(tx.request_payload.dlc in (6, 8) for tx in transactions))
                self.assertTrue(all(tx.response_payload.dlc == 8 for tx in transactions))

    def test_no_scan_fixtures_contain_no_frames(self) -> None:
        baseline = parse_candump_file(ARTIFACTS / "can-baseline-20260823.log")
        self.assertEqual(baseline, [])

    def test_parser_rejects_dropped_or_reordered_frames(self) -> None:
        with self.assertRaises(ValueError):
            extract_transactions(
                [
                    CanFrame(0x122, b"\x00" * 8),
                    CanFrame(0x123, b"\x00" * 6),
                    CanFrame(0x123, b"\x00" * 8),
                    CanFrame(0x122, b"\x00" * 8),
                ]
            )

    def test_parser_rejects_bad_declared_dlc(self) -> None:
        with self.assertRaisesRegex(ValueError, "declared DLC"):
            parse_candump("<0x122> [8] 00 01")

    def test_parser_preserves_transaction_bytes_without_assigning_semantics(self) -> None:
        frames = parse_candump(
            "\n".join(
                (
                    "<0x122> [8] 1d 13 00 00 45 3f c8 2f",
                    "<0x123> [6] 21 ca 20 fe 41 68",
                    "<0x122> [8] 00 00 e2 ed 88 7a c0 16",
                    "<0x123> [8] 39 7c 83 fc 0c 6c ad e7",
                )
            )
        )
        transaction = validate_transaction(frames)
        self.assertEqual(transaction.request_sequence_prefix, bytes.fromhex("1d130000"))
        self.assertEqual(transaction.response_sequence_prefix, bytes.fromhex("0000e2ed"))
        self.assertEqual(transaction.request_payload.data, bytes.fromhex("21ca20fe4168"))


if __name__ == "__main__":
    unittest.main()
