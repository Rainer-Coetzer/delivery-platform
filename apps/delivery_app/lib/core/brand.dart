// The brand from GET /config (config/brand.yaml on the server) and the
// theme built from it. A buyer rebrands by editing that file: nothing in
// the app names a colour, a currency or a tile server.
//
// Token rules (docs/CONFIGURATION.md, "How the app uses your colours"):
// - primary is required; secondary, background and text are optional.
// - Anything left out comes from Material 3's palette seeded by primary.
// - The colour of words on primary, secondary, and on a background given
//   without text, is picked here: black or white, whichever contrasts
//   more. So a brand can't make its own buttons unreadable.
// - text on background is checked by the server at boot (WCAG AA, 4.5:1),
//   so it isn't re-checked here.
import 'dart:math' as math;

import 'package:flutter/material.dart';

class Currency {
  final String code;
  final String symbol;
  final int minorUnits;

  const Currency({required this.code, required this.symbol, required this.minorUnits});

  factory Currency.fromJson(Map<String, dynamic> json) => Currency(
        code: json['code'] as String,
        symbol: json['symbol'] as String,
        minorUnits: json['minorUnits'] as int? ?? 2,
      );

  /// Minor units written the way the server writes them
  /// (pkg/brand.FormatMoney): 4300 -> "N$43.00", -250 -> "-R2.50".
  /// Integer arithmetic only: money is never a double.
  String format(int minor) {
    final neg = minor < 0;
    final abs = minor.abs();
    var out = symbol;
    if (minorUnits == 0) {
      out += '$abs';
    } else {
      final div = math.pow(10, minorUnits).toInt();
      out += '${abs ~/ div}.${(abs % div).toString().padLeft(minorUnits, '0')}';
    }
    return neg ? '-$out' : out;
  }
}

class MapSettings {
  final double lat;
  final double lng;
  final double zoom;
  final String tilesUrl;
  final String attribution;
  final int maxZoom;

  const MapSettings({
    required this.lat,
    required this.lng,
    required this.zoom,
    required this.tilesUrl,
    required this.attribution,
    required this.maxZoom,
  });

  factory MapSettings.fromJson(Map<String, dynamic> json) {
    final centre = json['centre'] as Map<String, dynamic>;
    final tiles = json['tiles'] as Map<String, dynamic>;
    return MapSettings(
      lat: (centre['lat'] as num).toDouble(),
      lng: (centre['lng'] as num).toDouble(),
      zoom: (json['zoom'] as num).toDouble(),
      tilesUrl: tiles['url'] as String,
      attribution: tiles['attribution'] as String,
      // 19 is what most raster providers serve; the brand can say otherwise.
      maxZoom: tiles['maxZoom'] as int? ?? 19,
    );
  }
}

class Brand {
  final String name;
  final String tagline;
  final String logoUrl; // relative to the gateway; '' for none
  final String logoType; // media type, e.g. image/svg+xml
  final Color primary;
  final Color? secondary;
  final Color? background;
  final Color? text;
  final String font; // '' for the platform's default
  final Currency currency;
  final MapSettings map;

  const Brand({
    required this.name,
    required this.tagline,
    required this.logoUrl,
    required this.logoType,
    required this.primary,
    this.secondary,
    this.background,
    this.text,
    required this.font,
    required this.currency,
    required this.map,
  });

  factory Brand.fromJson(Map<String, dynamic> json) {
    final colours = json['colours'] as Map<String, dynamic>;
    Color? optional(String key) {
      final v = colours[key] as String?;
      return v == null || v.isEmpty ? null : parseHex(v);
    }

    return Brand(
      name: json['name'] as String,
      tagline: json['tagline'] as String? ?? '',
      logoUrl: json['logoUrl'] as String? ?? '',
      logoType: json['logoType'] as String? ?? '',
      primary: parseHex(colours['primary'] as String),
      secondary: optional('secondary'),
      background: optional('background'),
      text: optional('text'),
      font: json['font'] as String? ?? '',
      currency: Currency.fromJson(json['currency'] as Map<String, dynamic>),
      map: MapSettings.fromJson(json['map'] as Map<String, dynamic>),
    );
  }
}

/// #RRGGBB to a Color. The server has already checked the format, so a
/// bad value here means the app is talking to something that isn't the
/// platform: fail loudly rather than paint it black.
Color parseHex(String hex) {
  if (!RegExp(r'^#[0-9A-Fa-f]{6}$').hasMatch(hex)) {
    throw FormatException('brand colour "$hex" is not #RRGGBB');
  }
  return Color(0xFF000000 | int.parse(hex.substring(1), radix: 16));
}

/// WCAG relative luminance; the same formula as pkg/brand/contrast.go.
double luminance(Color c) {
  double channel(double s) => s <= 0.03928 ? s / 12.92 : math.pow((s + 0.055) / 1.055, 2.4).toDouble();
  return 0.2126 * channel(c.r) + 0.7152 * channel(c.g) + 0.0722 * channel(c.b);
}

/// WCAG contrast ratio, 1 to 21.
double contrast(Color a, Color b) {
  final la = luminance(a), lb = luminance(b);
  return (math.max(la, lb) + 0.05) / (math.min(la, lb) + 0.05);
}

/// Black or white, whichever reads better on bg.
Color readableOn(Color bg) => contrast(bg, Colors.white) >= contrast(bg, Colors.black) ? Colors.white : Colors.black;

/// Corner radius for buttons, fields and cards. A design token, not a
/// brand setting: one number keeps every surface consistent.
const double radius = 12;

/// The app's theme from the brand. Light only for now.
ThemeData buildTheme(Brand brand) {
  final seeded = ColorScheme.fromSeed(seedColor: brand.primary);
  final secondary = brand.secondary ?? seeded.secondary;
  final surface = brand.background ?? seeded.surface;
  final onSurface = brand.text ?? (brand.background != null ? readableOn(surface) : seeded.onSurface);
  final branded = brand.background != null || brand.text != null;

  final scheme = seeded.copyWith(
    primary: brand.primary,
    onPrimary: readableOn(brand.primary),
    secondary: secondary,
    onSecondary: readableOn(secondary),
    surface: surface,
    onSurface: onSurface,
    // Muted text (hints, captions) sits between the two, so it keeps
    // the brand's hue and stays readable; M3's own when neither is set.
    onSurfaceVariant: branded ? Color.lerp(onSurface, surface, 0.3) : seeded.onSurfaceVariant,
  );
  final shape = RoundedRectangleBorder(borderRadius: BorderRadius.circular(radius));

  return ThemeData(
    useMaterial3: true,
    colorScheme: scheme,
    fontFamily: brand.font.isEmpty ? null : brand.font,
    scaffoldBackgroundColor: scheme.surface,
    appBarTheme: AppBarTheme(
      backgroundColor: scheme.surface,
      foregroundColor: scheme.onSurface,
      elevation: 0,
      scrolledUnderElevation: 1,
    ),
    // 48 px: the minimum comfortable touch target.
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(minimumSize: const Size(64, 48), shape: shape),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(minimumSize: const Size(64, 48), shape: shape),
    ),
    inputDecorationTheme: InputDecorationTheme(
      border: OutlineInputBorder(borderRadius: BorderRadius.circular(radius)),
    ),
    cardTheme: CardThemeData(
      elevation: 0,
      color: Color.lerp(scheme.surface, scheme.onSurface, 0.04),
      shape: shape,
    ),
  );
}
