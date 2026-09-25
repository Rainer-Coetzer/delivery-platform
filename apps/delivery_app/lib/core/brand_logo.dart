import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_svg/flutter_svg.dart';

import 'brand.dart';
import 'providers.dart';

/// The brand's logo from the gateway (/brand/logo), or, when the brand has
/// none or it fails to load, its name's first letter in the primary colour.
/// A missing logo never breaks a screen.
class BrandLogo extends ConsumerWidget {
  final Brand brand;
  final double size;
  const BrandLogo({super.key, required this.brand, this.size = 64});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final fallback = _Initial(brand: brand, size: size);
    if (brand.logoUrl.isEmpty) return fallback;
    final url = ref.watch(apiClientProvider).dio.options.baseUrl + brand.logoUrl;
    final label = '${brand.name} logo';
    if (brand.logoType == 'image/svg+xml') {
      return SvgPicture.network(
        url,
        width: size,
        height: size,
        semanticsLabel: label,
        placeholderBuilder: (_) => SizedBox(width: size, height: size),
        errorBuilder: (_, __, ___) => fallback,
      );
    }
    return Image.network(url, width: size, height: size, semanticLabel: label, errorBuilder: (_, __, ___) => fallback);
  }
}

class _Initial extends StatelessWidget {
  final Brand brand;
  final double size;
  const _Initial({required this.brand, required this.size});

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Semantics(
      label: '${brand.name} logo',
      child: CircleAvatar(
        radius: size / 2,
        backgroundColor: scheme.primary,
        child: Text(
          brand.name.isEmpty ? '?' : brand.name.characters.first.toUpperCase(),
          style: TextStyle(color: scheme.onPrimary, fontSize: size * 0.45, fontWeight: FontWeight.w600),
        ),
      ),
    );
  }
}
