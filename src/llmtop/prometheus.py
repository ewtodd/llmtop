"""Parser for the Prometheus text exposition format.

Only the subset needed to read engine /metrics endpoints is implemented:
comments, optional labels, values, optional timestamps.
"""

from __future__ import annotations

from dataclasses import dataclass, field


@dataclass(frozen=True)
class Sample:
    name: str
    labels: dict[str, str] = field(default_factory=dict)
    value: float = 0.0

    def has_labels(self, expected: dict[str, str]) -> bool:
        for key, value in expected.items():
            if self.labels.get(key) != value:
                return False
        return True


def parse_prometheus(text: str) -> list[Sample]:
    samples: list[Sample] = []
    for line in text.splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        parsed = _parse_sample_line(line)
        if parsed is not None:
            samples.append(parsed)
    return samples


def _parse_sample_line(line: str) -> Sample | None:
    name, rest = _split_metric_and_rest(line)
    if not name:
        return None
    if rest.startswith("{"):
        close = _matching_brace(rest)
        if close < 0:
            return None
        labels = _parse_labels(rest[1:close])
        remainder = rest[close + 1 :].strip()
    else:
        labels = {}
        remainder = rest
    if not remainder:
        return None
    value_text = remainder.split()[0]
    parsed_value = _parse_value(value_text)
    if parsed_value is None:
        return None
    return Sample(name=name, labels=labels, value=parsed_value)


def _split_metric_and_rest(line: str) -> tuple[str, str]:
    for index, char in enumerate(line):
        if char == "{" or char.isspace():
            return line[:index].strip(), line[index:].strip()
    return line.strip(), ""


def _matching_brace(text: str) -> int:
    quoted = False
    escaped = False
    for index, char in enumerate(text):
        if escaped:
            escaped = False
            continue
        if char == "\\":
            escaped = True
            continue
        if char == '"':
            quoted = not quoted
            continue
        if char == "}" and not quoted:
            return index
    return -1


def _parse_labels(text: str) -> dict[str, str]:
    labels: dict[str, str] = {}
    index = 0
    length = len(text)
    while index < length:
        while index < length and text[index] in ", \t":
            index += 1
        if index >= length:
            break
        equals = text.find("=", index)
        if equals < 0:
            break
        key = text[index:equals].strip()
        index = equals + 1
        if index < length and text[index] == '"':
            value, index = _read_quoted(text, index)
        else:
            comma = text.find(",", index)
            if comma < 0:
                comma = length
            value = text[index:comma].strip()
            index = comma
        if key:
            labels[key] = value
    return labels


def _read_quoted(text: str, index: int) -> tuple[str, int]:
    index += 1
    chunks: list[str] = []
    while index < len(text):
        char = text[index]
        if char == "\\" and index + 1 < len(text):
            next_char = text[index + 1]
            chunks.append({"n": "\n", '"': '"', "\\": "\\"}.get(next_char, next_char))
            index += 2
            continue
        if char == '"':
            return "".join(chunks), index + 1
        chunks.append(char)
        index += 1
    return "".join(chunks), index


def _parse_value(text: str) -> float | None:
    lowered = text.lower()
    if lowered in ("nan", "+nan", "-nan"):
        return float("nan")
    if lowered in ("inf", "+inf"):
        return float("inf")
    if lowered == "-inf":
        return float("-inf")
    try:
        return float(text)
    except ValueError:
        return None


def total(samples: list[Sample], name: str, **labels: str) -> float:
    total_value = 0.0
    for sample in samples:
        if sample.name == name and sample.has_labels(labels):
            total_value += sample.value
    return total_value


def value(samples: list[Sample], name: str, **labels: str) -> float | None:
    for sample in samples:
        if sample.name == name and sample.has_labels(labels):
            return sample.value
    return None


def label_values(samples: list[Sample], name: str, label: str) -> list[str]:
    seen: list[str] = []
    for sample in samples:
        if sample.name != name:
            continue
        candidate = sample.labels.get(label)
        if candidate is not None and candidate not in seen:
            seen.append(candidate)
    return seen
