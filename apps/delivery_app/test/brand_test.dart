import 'package:delivery_app/core/brand.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

// The brand part of GET /config (pkg/brand.Brand as JSON), as a buyer with
// only a primary colour would send it.
Map<String, dynamic> brandJson({Map<String, String> colours = const {'primary': '#E4572E'}}) => {
      'name': 'Test Couriers',
      'colours': colours,
      'currency': {'code': 'ZAR', 'symbol': 'R', 'minorUnits': 2},
      'map': {
        'centre': {'lat': -26.2, 'lng': 28.04},
        'zoom': 12,
        'tiles': {'url': 'https://tiles.example.com/{z}/{x}/{y}.png', 'attribution': '© Example'},
      },
    };

void main() {
  test('money is written the way the server writes it', () {
    const nad = Currency(code: 'NAD', symbol: 'N\$', minorUnits: 2);
    // The same cases as pkg/brand TestFormatMoney.
    expect(nad.format(4300), 'N\$43.00');
    expect(nad.format(5), 'N\$0.05');
    expect(const Currency(code: 'ZAR', symbol: 'R', minorUnits: 2).format(-250), '-R2.50');
    expect(const Currency(code: 'JPY', symbol: '¥', minorUnits: 0).format(1500), '¥1500');
    expect(const Currency(code: 'BHD', symbol: 'BD', minorUnits: 3).format(1234), 'BD1.234');
  });

  test('a typed price becomes minor units without floating point', () {
    const nad = Currency(code: 'NAD', symbol: 'N\$', minorUnits: 2);
    expect(nad.parse('65'), 6500);
    expect(nad.parse(' 65.5 '), 6550);
    expect(nad.parse('65,50'), 6550); // a decimal comma (af)
    expect(nad.parse('0.29'), 29); // 0.29 * 100 is 28.999... as a double
    expect(nad.parse('65.'), 6500);
    for (final bad in ['', 'abc', '-5', '65.505', '6 5', '1e3', '.5', '99999999999999999999']) {
      expect(nad.parse(bad), isNull, reason: bad);
    }
    const jpy = Currency(code: 'JPY', symbol: '¥', minorUnits: 0);
    expect(jpy.parse('1500'), 1500);
    expect(jpy.parse('1500.5'), isNull);
    expect(nad.example, '12.50');
    expect(jpy.example, '12');
    expect(const Currency(code: 'BHD', symbol: 'BD', minorUnits: 3).example, '12.500');
  });

  test('contrast matches the server (pkg/brand/contrast.go)', () {
    expect(contrast(Colors.black, Colors.white), closeTo(21, 0.05));
    expect(contrast(parseHex('#1D1D1F'), parseHex('#FFFBF7')), closeTo(16.3, 0.05));
    expect(contrast(parseHex('#777777'), parseHex('#888888')), closeTo(1.3, 0.05));
  });

  test('a bad colour fails loudly instead of painting black', () {
    expect(() => parseHex('red'), throwsFormatException);
    expect(() => parseHex('#12345'), throwsFormatException);
  });

  test('words on the brand colours are always readable', () {
    // Dark and light primaries, and a mid one where the choice is close.
    for (final hex in ['#112233', '#FFD400', '#E4572E', '#17A398', '#777777']) {
      final c = parseHex(hex);
      final on = readableOn(c);
      final other = on == Colors.white ? Colors.black : Colors.white;
      expect(contrast(c, on) >= contrast(c, other), isTrue, reason: hex);
    }
    final dark = buildTheme(Brand.fromJson(brandJson(colours: {'primary': '#112233'})));
    expect(dark.colorScheme.onPrimary, Colors.white);
    final yellow = buildTheme(Brand.fromJson(brandJson(colours: {'primary': '#FFD400', 'secondary': '#003366'})));
    expect(yellow.colorScheme.onPrimary, Colors.black);
    expect(yellow.colorScheme.onSecondary, Colors.white);
  });

  test('only primary given: the rest comes from the seeded palette', () {
    final brand = Brand.fromJson(brandJson());
    final theme = buildTheme(brand);
    final seeded = ColorScheme.fromSeed(seedColor: parseHex('#E4572E'));
    expect(theme.colorScheme.primary, parseHex('#E4572E'));
    expect(theme.colorScheme.surface, seeded.surface);
    expect(theme.colorScheme.onSurface, seeded.onSurface);
    expect(theme.colorScheme.onSurfaceVariant, seeded.onSurfaceVariant);
    expect(theme.scaffoldBackgroundColor, seeded.surface);
    expect(brand.font, '');
    expect(theme.textTheme.bodyMedium?.fontFamily, isNot('')); // platform default
  });

  test('a background without text gets readable text', () {
    final theme = buildTheme(Brand.fromJson(brandJson(colours: {'primary': '#E4572E', 'background': '#101418'})));
    expect(theme.colorScheme.surface, parseHex('#101418'));
    expect(theme.colorScheme.onSurface, Colors.white);
    // Muted text keeps enough contrast for captions (WCAG AA large text).
    expect(contrast(theme.colorScheme.onSurfaceVariant, theme.colorScheme.surface) >= 3, isTrue);
  });

  test('the brand\'s own background and text are used as given', () {
    final theme = buildTheme(Brand.fromJson(brandJson(colours: {'primary': '#E4572E', 'background': '#FFFBF7', 'text': '#1D1D1F'})));
    expect(theme.colorScheme.surface, parseHex('#FFFBF7'));
    expect(theme.colorScheme.onSurface, parseHex('#1D1D1F'));
    expect(contrast(theme.colorScheme.onSurfaceVariant, theme.colorScheme.surface) >= 4.5, isTrue);
  });

  test('map settings, with the provider\'s usual max zoom by default', () {
    final m = Brand.fromJson(brandJson()).map;
    expect(m.tilesUrl, 'https://tiles.example.com/{z}/{x}/{y}.png');
    expect(m.attribution, '© Example');
    expect(m.maxZoom, 19);
    expect(m.zoom, 12);
  });
}
