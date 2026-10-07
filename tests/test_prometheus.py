from llmtop.prometheus import label_values, parse_prometheus, total, value


SAMPLE = """
# HELP vllm:prompt_tokens_total prompt tokens
# TYPE vllm:prompt_tokens_total counter
vllm:prompt_tokens_total{engine="0",model_name="qwen3.8-27b"} 3.464046e+06
vllm:num_requests_running{engine="0",model_name="qwen3.8-27b"} 2.0
llamacpp:kv_cache_usage_ratio 0.16214497884114584
gufo_device_lost_total 0
metric_with_escaped{label="a\\"b\\\\c\\nd"} 1.5
plain_metric 42 1234567890
nan_metric NaN
"""


def test_parse_samples():
    samples = parse_prometheus(SAMPLE)
    assert total(samples, "vllm:prompt_tokens_total") == 3.464046e06
    assert value(samples, "vllm:num_requests_running") == 2.0
    assert value(samples, "llamacpp:kv_cache_usage_ratio") == 0.16214497884114584
    assert value(samples, "plain_metric") == 42.0


def test_label_values_and_filters():
    samples = parse_prometheus(SAMPLE)
    assert label_values(samples, "vllm:prompt_tokens_total", "model_name") == [ "qwen3.8-27b" ]
    assert total(samples, "vllm:prompt_tokens_total", model_name="other") == 0.0
    assert total(samples, "vllm:prompt_tokens_total", model_name="qwen3.8-27b") == 3.464046e06


def test_escaped_labels():
    samples = parse_prometheus(SAMPLE)
    escaped = value(samples, "metric_with_escaped")
    assert escaped == 1.5
    labeled = [ sample for sample in samples if sample.name == "metric_with_escaped" ]
    assert labeled[0].labels["label"] == 'a"b\\c\nd'


def test_comments_blanks_and_bad_lines():
    samples = parse_prometheus("\n# comment\n\nbroken line without value\nok_metric 1\n")
    assert [ sample.name for sample in samples ] == [ "ok_metric" ]
