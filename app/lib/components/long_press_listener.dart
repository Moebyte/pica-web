import 'dart:async';

import 'package:flutter/gestures.dart';
import 'package:flutter/widgets.dart';

/// Detects a long press with a passive [Listener].
///
/// GestureDetector around PhotoView/scrollables can starve the gesture arena
/// and make the reader unresponsive; a Listener only observes pointer events
/// and never competes for the gesture, so paging/zoom keep working. The timer
/// is cancelled as soon as the pointer moves or lifts.
class LongPressPointerDetector extends StatefulWidget {
  const LongPressPointerDetector({
    super.key,
    required this.child,
    required this.onLongPress,
    this.delay = const Duration(milliseconds: 550),
    this.moveTolerance = 12,
  });

  final Widget child;
  final ValueChanged<Offset> onLongPress;
  final Duration delay;
  final double moveTolerance;

  @override
  State<LongPressPointerDetector> createState() =>
      _LongPressPointerDetectorState();
}

class _LongPressPointerDetectorState extends State<LongPressPointerDetector> {
  Timer? _timer;
  Offset? _start;

  void _onDown(PointerDownEvent event) {
    _cancel();
    _start = event.position;
    _timer = Timer(widget.delay, () {
      final start = _start;
      _start = null;
      _timer = null;
      if (start != null && mounted) {
        widget.onLongPress(start);
      }
    });
  }

  void _onMove(PointerMoveEvent event) {
    final start = _start;
    if (start != null &&
        (event.position - start).distance > widget.moveTolerance) {
      _cancel();
    }
  }

  void _cancel() {
    _timer?.cancel();
    _timer = null;
    _start = null;
  }

  @override
  void dispose() {
    _cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Listener(
      behavior: HitTestBehavior.translucent,
      onPointerDown: _onDown,
      onPointerMove: _onMove,
      onPointerUp: (_) => _cancel(),
      onPointerCancel: (_) => _cancel(),
      child: widget.child,
    );
  }
}
