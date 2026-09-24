// Smoke test: the app boots inside a ProviderScope and lands on the login
// screen when nobody is signed in.
//
// Keep this file named widget_test.dart. CI runs `flutter create .` to
// generate the platform folders, and that writes a default
// test/widget_test.dart (referencing a MyApp class that doesn't exist here)
// unless a file with this name is already present.
import 'package:delivery_app/main.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('boots to the login screen when signed out', (tester) async {
    await tester.pumpWidget(const ProviderScope(child: DeliveryApp()));
    await tester.pumpAndSettle();

    expect(find.text('Delivery Platform'), findsOneWidget);
    expect(find.widgetWithText(FilledButton, 'Log in'), findsOneWidget);
    expect(find.text("Don't have an account? Register"), findsOneWidget);
  });
}
