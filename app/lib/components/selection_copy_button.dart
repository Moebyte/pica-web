import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:pica_comic/components/components.dart';
import 'package:pica_comic/tools/translations.dart';

/// Shows a small floating "copy" action while text is selected.
///
/// Flutter Web on Android/iOS does not reliably show the Material selection
/// toolbar (there is no native DOM text to right-click), so this gives touch
/// users a guaranteed way to copy whatever they selected.
class SelectionCopyButton extends StatelessWidget {
  const SelectionCopyButton({super.key, this.onCopied});

  static final ValueNotifier<String> selectedText = ValueNotifier("");

  static void update(String? text) {
    selectedText.value = (text ?? "").trim();
  }

  final VoidCallback? onCopied;

  static DateTime? _lastCopyAt;

  void _copy(BuildContext context, String text) {
    if (text.isEmpty) return;
    final now = DateTime.now();
    if (_lastCopyAt != null &&
        now.difference(_lastCopyAt!) < const Duration(milliseconds: 600)) {
      return;
    }
    _lastCopyAt = now;
    Clipboard.setData(ClipboardData(text: text));
    showToast(message: "已复制".tl);
    update(null);
    onCopied?.call();
  }

  @override
  Widget build(BuildContext context) {
    return ValueListenableBuilder<String>(
      valueListenable: selectedText,
      builder: (context, text, child) {
        final visible = text.isNotEmpty;
        return Positioned(
          left: 0,
          right: 0,
          bottom: MediaQuery.of(context).padding.bottom + 88,
          child: IgnorePointer(
            ignoring: !visible,
            child: AnimatedOpacity(
              opacity: visible ? 1 : 0,
              duration: const Duration(milliseconds: 120),
              child: Center(
                child: Listener(
                  // Copy on pointer down: tapping the button otherwise clears
                  // the SelectableRegion selection before onTap fires.
                  onPointerDown: (_) => _copy(context, text),
                  child: Material(
                    elevation: 6,
                    color: Theme.of(context).colorScheme.inverseSurface,
                    borderRadius: BorderRadius.circular(24),
                    child: InkWell(
                      borderRadius: BorderRadius.circular(24),
                      onTap: () => _copy(context, text),
                      child: Padding(
                        padding: const EdgeInsets.symmetric(
                            horizontal: 20, vertical: 10),
                        child: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Icon(
                              Icons.copy_rounded,
                              size: 18,
                              color: Theme.of(context)
                                  .colorScheme
                                  .onInverseSurface,
                            ),
                            const SizedBox(width: 8),
                            Text(
                              "复制".tl,
                              style: TextStyle(
                                fontSize: 14,
                                color: Theme.of(context)
                                    .colorScheme
                                    .onInverseSurface,
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
                  ),
                ),
              ),
            ),
          ),
        );
      },
    );
  }
}
