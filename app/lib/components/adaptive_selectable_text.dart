import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

/// Text that can be selected with the platform's native mechanism on native
/// builds, but delegates to the app-level [SelectionArea] on Web.
///
/// Nesting [SelectableText] inside the Web [SelectionArea] creates a second
/// selection region; CanvasKit then shows unreliable (or missing) copy UI.
/// Plain [Text] participates in the outer selection area and gets the floating
/// copy action instead.
class AdaptiveSelectableText extends StatelessWidget {
  const AdaptiveSelectableText(
    this.data, {
    super.key,
    this.style,
    this.textAlign,
    this.maxLines,
  });

  final String data;
  final TextStyle? style;
  final TextAlign? textAlign;
  final int? maxLines;

  @override
  Widget build(BuildContext context) {
    if (kIsWeb) {
      return Text(
        data,
        style: style,
        textAlign: textAlign,
        maxLines: maxLines,
        overflow: maxLines != null ? TextOverflow.ellipsis : null,
      );
    }
    return SelectableText(
      data,
      style: style,
      textAlign: textAlign,
      maxLines: maxLines,
    );
  }
}
