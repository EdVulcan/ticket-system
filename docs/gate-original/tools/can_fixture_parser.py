"""Offline parser for the original gate's passive SocketCAN captures.

This module deliberately stops at the observable transaction envelope.  The
0x123 payload is protected by the original program's commKey/MD5/XOR scheme,
and this parser does not contain keys, talk to a CAN interface, or attempt to
replay a frame.  It is therefore suitable for fixture validation only.
"""

from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
import re
from typing import Iterable, Sequence


_FRAME_RE = re.compile(
    r"^\s*<0x(?P<can_id>[0-9a-fA-F]+)>\s+"
    r"\[(?P<dlc>\d+)\]\s*(?P<data>[0-9a-fA-F ]*)\s*$"
)


@dataclass(frozen=True)
class CanFrame:
    """One candump frame, retaining only fields needed by the fixtures."""

    can_id: int
    data: bytes

    @property
    def dlc(self) -> int:
        return len(self.data)


@dataclass(frozen=True)
class CanTransaction:
    """The four-frame request/response envelope observed for a valid scan."""

    request_header: CanFrame
    request_payload: CanFrame
    response_header: CanFrame
    response_payload: CanFrame

    @property
    def request_sequence_prefix(self) -> bytes:
        """Raw first four bytes of the request 0x122 frame.

        The current evidence supports treating these as an opaque transaction
        prefix.  No byte order or vendor field name is assigned here.
        """

        return self.request_header.data[:4]

    @property
    def response_sequence_prefix(self) -> bytes:
        """Raw first four bytes of the response 0x122 frame."""

        return self.response_header.data[:4]


def parse_candump(text: str) -> list[CanFrame]:
    """Parse the strict candump lines used by the archived fixtures.

    Comments and blank lines are ignored.  Malformed frame lines fail closed
    instead of being silently dropped, because a dropped frame could make an
    invalid sequence look like a valid four-frame transaction.
    """

    frames: list[CanFrame] = []
    for line_number, raw_line in enumerate(text.splitlines(), start=1):
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        match = _FRAME_RE.match(line)
        if match is None:
            raise ValueError(f"line {line_number}: invalid candump frame: {raw_line!r}")

        data_tokens = match.group("data").split()
        dlc = int(match.group("dlc"), 10)
        if len(data_tokens) != dlc:
            raise ValueError(
                f"line {line_number}: declared DLC {dlc} but found {len(data_tokens)} bytes"
            )
        frames.append(
            CanFrame(
                can_id=int(match.group("can_id"), 16),
                data=bytes(int(token, 16) for token in data_tokens),
            )
        )
    return frames


def parse_candump_file(path: str | Path) -> list[CanFrame]:
    """Read and parse one archived candump capture."""

    return parse_candump(Path(path).read_text(encoding="utf-8"))


def validate_transaction(frames: Sequence[CanFrame]) -> CanTransaction:
    """Validate one observed four-frame transaction.

    The accepted envelope is exactly ``0x122, 0x123, 0x122, 0x123``.  The
    protected business frame may be six bytes on the request and eight bytes
    on the response, matching all current valid-ticket captures.  This is a
    structural check, not a claim that the response means a physical opening.
    """

    if len(frames) != 4:
        raise ValueError(f"expected four frames, got {len(frames)}")

    expected_ids = (0x122, 0x123, 0x122, 0x123)
    actual_ids = tuple(frame.can_id for frame in frames)
    if actual_ids != expected_ids:
        expected = " -> ".join(f"0x{can_id:03x}" for can_id in expected_ids)
        actual = " -> ".join(f"0x{can_id:03x}" for can_id in actual_ids)
        raise ValueError(f"unexpected transaction IDs: {actual}; expected {expected}")

    request_header, request_payload, response_header, response_payload = frames
    if request_header.dlc != 8 or response_header.dlc != 8:
        raise ValueError("both 0x122 transaction headers must have DLC 8")
    if request_payload.dlc not in (6, 8):
        raise ValueError("request 0x123 payload must have DLC 6 or 8")
    if response_payload.dlc != 8:
        raise ValueError("response 0x123 payload must have DLC 8")

    return CanTransaction(
        request_header=request_header,
        request_payload=request_payload,
        response_header=response_header,
        response_payload=response_payload,
    )


def extract_transactions(frames: Iterable[CanFrame]) -> list[CanTransaction]:
    """Validate a capture made of contiguous four-frame transactions."""

    frame_list = list(frames)
    if len(frame_list) % 4:
        raise ValueError(
            f"capture has {len(frame_list)} frames; a valid fixture must contain whole transactions"
        )
    return [
        validate_transaction(frame_list[offset : offset + 4])
        for offset in range(0, len(frame_list), 4)
    ]


def summarize_capture(path: str | Path) -> dict[str, object]:
    """Return a small, JSON-friendly structural summary for a capture."""

    frames = parse_candump_file(path)
    transactions = extract_transactions(frames) if frames else []
    return {
        "path": str(path),
        "frame_count": len(frames),
        "transaction_count": len(transactions),
        "ids": [f"0x{frame.can_id:03x}" for frame in frames],
        "request_lengths": [tx.request_payload.dlc for tx in transactions],
        "response_lengths": [tx.response_payload.dlc for tx in transactions],
    }


if __name__ == "__main__":
    import argparse
    import json

    parser = argparse.ArgumentParser(description="Validate an archived passive CAN capture")
    parser.add_argument("capture", type=Path)
    args = parser.parse_args()
    print(json.dumps(summarize_capture(args.capture), ensure_ascii=False, indent=2))
