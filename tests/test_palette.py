from llmtop.palette import (
    RGB,
    TerminalColors,
    build_palette,
    gray_scale,
    luminance,
    muted_text,
    parse_osc_color,
    polarity,
    theme_name,
)


def test_parse_osc_color_16_bit_and_8_bit():
    assert parse_osc_color("rgb:ffff/0000/8080") == RGB(255, 0, 128)
    assert parse_osc_color("rgb:ff/00/80") == RGB(255, 0, 128)
    assert parse_osc_color("\033]11;rgb:ffff/ffff/ffff\033\\") == RGB(255, 255, 255)


def test_parse_osc_color_hex_and_invalid():
    assert parse_osc_color("#102030") == RGB(16, 32, 48)
    assert parse_osc_color("") is None
    assert parse_osc_color("not a color") is None


def test_polarity_and_luminance():
    assert luminance(RGB(255, 255, 255)) == 255.0
    assert polarity(RGB(0, 0, 0)) == "dark"
    assert polarity(RGB(255, 255, 255)) == "light"


def test_gray_scale_is_monotonic():
    dark = gray_scale(RGB(20, 20, 40), is_dark=True)
    light = gray_scale(RGB(240, 240, 240), is_dark=False)
    dark_values = [ luminance(dark[index]) for index in sorted(dark) ]
    light_values = [ luminance(light[index]) for index in sorted(light) ]
    assert dark_values == sorted(dark_values)
    assert light_values == sorted(light_values, reverse=True)


def test_muted_text_contrasts_with_polarity():
    dark = muted_text(RGB(18, 18, 18), is_dark=True)
    light = muted_text(RGB(250, 250, 250), is_dark=False)
    assert luminance(dark) > luminance(RGB(18, 18, 18))
    assert luminance(light) < luminance(RGB(250, 250, 250))


def test_build_palette_keys_are_hex():
    palette = build_palette(TerminalColors(background=RGB(18, 18, 18), foreground=None))
    assert set(palette) == {
        "panel",
        "surface",
        "boost",
        "panel-lighten-1",
        "border-blurred",
        "border",
        "text-muted",
    }
    assert all(value.startswith("#") and len(value) == 7 for value in palette.values())


def test_theme_name_tracks_terminal_polarity():
    assert theme_name(None) == "ansi-dark"
    assert theme_name(TerminalColors(RGB(0, 0, 0), None)) == "ansi-dark"
    assert theme_name(TerminalColors(RGB(255, 255, 255), None)) == "ansi-light"
