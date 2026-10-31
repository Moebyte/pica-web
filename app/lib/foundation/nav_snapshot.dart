/// Restores the in-app page stack after a browser reload.
///
/// Flutter Web keeps the whole navigation stack in memory only, so a refresh
/// drops the user back on the home page. [NavSnapshot] records a lightweight
/// stack of page descriptors while the user navigates, mirrors it into
/// `sessionStorage` (debounced), and replays it after the first frame.
///
/// Only pages are recorded (dialogs, bottom sheets and overlays are not).
library;

import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:pica_comic/comic_source/comic_source.dart';
import 'package:pica_comic/foundation/app.dart';
import 'package:pica_comic/foundation/log.dart';
import 'package:pica_comic/foundation/nav_session_storage.dart';
import 'package:pica_comic/foundation/web_history_bridge.dart';
import 'package:pica_comic/foundation/web_runtime.dart';
import 'package:pica_comic/network/eh_network/eh_main_network.dart';
import 'package:pica_comic/network/htmanga_network/htmanga_main_network.dart';
import 'package:pica_comic/network/hitomi_network/hitomi_main_network.dart';
import 'package:pica_comic/network/jm_network/jm_network.dart';
import 'package:pica_comic/network/nhentai_network/nhentai_main_network.dart';
import 'package:pica_comic/network/picacg_network/methods.dart';
import 'package:pica_comic/pages/comic_page.dart';
import 'package:pica_comic/pages/download_page.dart';
import 'package:pica_comic/pages/history_page.dart';
import 'package:pica_comic/pages/reader/comic_reading_page.dart';
import 'package:pica_comic/pages/search_result_page.dart';
import 'package:pica_comic/pages/settings/settings_page.dart';

class _SnapshotEntry {
  _SnapshotEntry(this.data);

  final Map<String, dynamic> data;
}

class NavSnapshot {
  static const _storageKey = "pica_nav_snapshot_v1";

  static bool _enabled = false;
  static final List<_SnapshotEntry> _stack = [];
  static List<Map<String, dynamic>> _restorePlan = [];
  static Timer? _saveTimer;
  static bool _replaying = false;
  static bool _lifecycleInstalled = false;

  static bool get enabled => _enabled;

  /// Reads the snapshot before the first frame so [restoredMainTab] can be
  /// passed to `MainPage`. The replay itself runs after the first frame.
  static void init() {
    if (!kIsWeb) return;
    _enabled = true;
    if (!_lifecycleInstalled) {
      _lifecycleInstalled = true;
      // `pagehide` / hidden tabs: flush pending writes immediately, otherwise
      // the last ~300ms of navigation could be lost during a refresh.
      addPageLifecycleListener((hidden) {
        if (hidden) flush();
      });
    }
    try {
      final raw = readSessionValue(_storageKey);
      if (raw == null || raw.isEmpty) return;
      final decoded = jsonDecode(raw);
      if (decoded is! List) {
        removeSessionValue(_storageKey);
        return;
      }
      _restorePlan = decoded
          .whereType<Map>()
          .map((e) => Map<String, dynamic>.from(e))
          .where(_isValidEntry)
          .toList();
      LogManager.addLog(LogLevel.info, "NavSnapshot",
          "loaded restore plan with ${_restorePlan.length} entries");
    } catch (e, s) {
      _restorePlan = [];
      removeSessionValue(_storageKey);
      LogManager.addLog(
          LogLevel.error, "NavSnapshot", "read snapshot failed: $e\n$s");
    }
  }

  /// Main page tab from the previous session, if any.
  static int? get restoredMainTab {
    for (final entry in _restorePlan) {
      if (entry["kind"] == "main") {
        final tab = entry["tab"];
        if (tab is int && tab >= 0 && tab < 4) return tab;
      }
    }
    return null;
  }

  static bool get hasRestorePlan => _restorePlan.isNotEmpty;

  /// Called when the app starts on the welcome page (first use): an old
  /// snapshot must not be replayed on top of onboarding.
  static void discardRestorePlan() {
    _restorePlan = [];
    removeSessionValue(_storageKey);
  }

  static bool _isValidEntry(Map<String, dynamic> entry) {
    final kind = entry["kind"];
    switch (kind) {
      case "main":
        return entry["tab"] is int;
      case "comic":
      case "reader":
        return entry["source"] is String && entry["id"] is String;
      case "search":
        return entry["source"] is String && entry["query"] is String;
      case "page":
        return entry["name"] is String;
      default:
        return false;
    }
  }

  // ---------------------------------------------------------------------
  // Recording
  // ---------------------------------------------------------------------

  /// Records a page. Returns a token to pass to [update] / [leave].
  static Object? enter(Map<String, dynamic> data) {
    if (!_enabled) return null;
    final entry = _SnapshotEntry(Map<String, dynamic>.from(data));
    _stack.add(entry);
    _scheduleSave();
    // If a back press at the root consumed the browser sentinel, opening any
    // new page re-arms it so back keeps working for the new stack.
    WebHistoryBridge.ensureSentinel();
    return entry;
  }

  static void update(Object? token, Map<String, dynamic> patch) {
    if (!_enabled || token is! _SnapshotEntry) return;
    token.data.addAll(patch);
    _scheduleSave();
  }

  /// Removes the page from the snapshot, wherever it is in the stack.
  static void leave(Object? token) {
    if (!_enabled || token is! _SnapshotEntry) return;
    _stack.remove(token);
    _scheduleSave();
  }

  static void setMainTab(int tab) {
    for (final entry in _stack.reversed) {
      if (entry.data["kind"] == "main") {
        entry.data["tab"] = tab;
        _scheduleSave();
        WebHistoryBridge.ensureSentinel();
        return;
      }
    }
  }

  static void _scheduleSave() {
    if (!_enabled) return;
    _saveTimer?.cancel();
    _saveTimer = Timer(const Duration(milliseconds: 300), flush);
  }

  /// Writes the current stack to sessionStorage now.
  static void flush() {
    if (!_enabled) return;
    _saveTimer?.cancel();
    _saveTimer = null;
    try {
      final json = jsonEncode(_stack.map((e) => e.data).toList());
      writeSessionValue(_storageKey, json);
    } catch (e, s) {
      LogManager.addLog(
          LogLevel.error, "NavSnapshot", "write snapshot failed: $e\n$s");
    }
  }

  // ---------------------------------------------------------------------
  // Descriptors
  // ---------------------------------------------------------------------

  static Map<String, dynamic> mainData(int tab) => {
        "kind": "main",
        "tab": tab,
      };

  static Map<String, dynamic> comicData(String source, String id) => {
        "kind": "comic",
        "source": source,
        "id": id,
      };

  static Map<String, dynamic> readerData({
    required String source,
    required String id,
    required int ep,
    required int page,
    String? detailId,
  }) =>
      {
        "kind": "reader",
        "source": source,
        "id": id,
        "ep": ep,
        "page": page,
        if (detailId != null) "detailId": detailId,
      };

  static Map<String, dynamic> searchData({
    required String source,
    required String query,
    required List<String> options,
  }) =>
      {
        "kind": "search",
        "source": source,
        "query": query,
        "options": options,
      };

  static Map<String, dynamic> pageData(String name) => {
        "kind": "page",
        "name": name,
      };

  // ---------------------------------------------------------------------
  // Replaying
  // ---------------------------------------------------------------------

  /// Replays the snapshot loaded in [init]. Call once after the first frame.
  static Future<void> replay() async {
    if (!_enabled || _replaying) return;
    final plan = List<Map<String, dynamic>>.from(_restorePlan);
    _restorePlan = [];
    if (plan.isEmpty) return;
    _replaying = true;
    try {
      // The base page (main tab) is restored by MainPage itself before the
      // first frame; without it we are on onboarding and must not replay.
      final onMain = _stack.any((e) => e.data["kind"] == "main");
      if (!onMain) {
        LogManager.addLog(LogLevel.info, "NavSnapshot",
            "skip replay: main page is not mounted");
        return;
      }

      // Stable order: main, list-like pages, comic detail, reader last.
      final ordered = List<Map<String, dynamic>>.from(plan);
      ordered.sort((a, b) => _priority(a).compareTo(_priority(b)));

      var pushedComic = false;
      for (final entry in ordered) {
        try {
          switch (entry["kind"]) {
            case "main":
              break;
            case "search":
              _pushIntoMain(_instantRoute(() => SearchResultPage(
                    keyword: entry["query"] as String,
                    sourceKey: entry["source"] as String,
                    options: (entry["options"] as List?)
                            ?.whereType<String>()
                            .toList() ??
                        const [],
                  )));
              break;
            case "page":
              final page = _pageFor(entry["name"] as String?);
              if (page != null) {
                _pushIntoMain(_instantRoute(() => page));
              }
              break;
            case "comic":
              pushedComic = true;
              _pushIntoMain(_instantRoute(() => ComicPage(
                    sourceKey: entry["source"] as String,
                    id: entry["id"] as String,
                  )));
              break;
            case "reader":
              final restored = await _restoreReader(entry);
              if (!restored && !pushedComic) {
                // Degrade to the detail page when the reader cannot rebuild.
                _pushIntoMain(_instantRoute(() => ComicPage(
                      sourceKey: entry["source"] as String,
                      id: entry["id"] as String,
                    )));
              }
              break;
          }
          // Let the new route settle before pushing the next one.
          await Future.delayed(const Duration(milliseconds: 30));
        } catch (e, s) {
          LogManager.addLog(LogLevel.error, "NavSnapshot",
              "replay ${entry["kind"]} failed: $e\n$s");
        }
      }
      LogManager.addLog(
          LogLevel.info, "NavSnapshot", "replayed ${ordered.length} entries");
    } finally {
      _replaying = false;
      flush();
    }
  }

  static int _priority(Map<String, dynamic> entry) {
    switch (entry["kind"]) {
      case "main":
        return 0;
      case "page":
      case "search":
        return 1;
      case "comic":
        return 2;
      case "reader":
        return 3;
      default:
        return 4;
    }
  }

  static Widget? _pageFor(String? name) {
    switch (name) {
      case "history":
        return const HistoryPage();
      case "download":
        return const DownloadPage();
      case "settings":
        return const SettingsPage();
      default:
        return null;
    }
  }

  static void _pushIntoMain(Route<void> route) {
    final nested = App.mainNavigatorKey?.currentState;
    if (nested != null) {
      nested.push(route);
    } else {
      App.navigatorKey.currentState?.push(route);
    }
  }

  static PageRoute<void> _instantRoute(Widget Function() builder) {
    return PageRouteBuilder<void>(
      transitionDuration: Duration.zero,
      reverseTransitionDuration: Duration.zero,
      pageBuilder: (context, animation, secondaryAnimation) => builder(),
      transitionsBuilder: (context, animation, secondaryAnimation, child) =>
          child,
    );
  }

  /// Rebuilds the reader for [entry]. Returns false when the source data
  /// cannot be loaded (the caller then falls back to the detail page).
  static Future<bool> _restoreReader(Map<String, dynamic> entry) async {
    final sourceKey = entry["source"] as String?;
    final id = entry["id"] as String?;
    if (sourceKey == null || id == null) return false;
    final ep = (entry["ep"] as num?)?.toInt() ?? 1;
    final page = (entry["page"] as num?)?.toInt() ?? 1;
    try {
      Widget? reader;
      switch (sourceKey) {
        case "picacg":
          final res = await network.getComicInfo(id);
          if (res.error) return false;
          final data = res.data;
          reader = ComicReadingPage.picacg(
            data.id,
            ep,
            data.eps,
            data.title,
            initialPage: page,
          );
          break;
        case "ehentai":
          final res = await EhNetwork().getGalleryInfo(id, false);
          if (res.error) return false;
          reader = ComicReadingPage.ehentai(res.data, initialPage: page);
          break;
        case "jm":
          final res = await JmNetwork().getComicInfo(id);
          if (res.error) return false;
          final data = res.data;
          reader =
              ComicReadingPage.jmComic(data, ep, initialPage: page);
          break;
        case "hitomi":
          final res = await HiNetwork().getComicInfo(id);
          if (res.error) return false;
          final data = res.data;
          reader = ComicReadingPage.hitomi(
            data,
            entry["detailId"] as String? ?? id,
            initialPage: page,
          );
          break;
        case "htmanga":
          final res = await HtmangaNetwork().getComicInfo(id);
          if (res.error) return false;
          final data = res.data;
          reader = ComicReadingPage.htmanga(
            data.target,
            data.title,
            initialPage: page,
          );
          break;
        case "nhentai":
          final res = await NhentaiNetwork().getComicInfo(id);
          if (res.error) return false;
          final data = res.data;
          reader = ComicReadingPage.nhentai(
            data.id,
            data.title,
            initialPage: page,
          );
          break;
        default:
          final source = ComicSource.find(sourceKey);
          final loader = source?.loadComicInfo;
          if (source == null || loader == null) return false;
          final res = await loader(id);
          if (res.error) return false;
          final data = res.data;
          reader = ComicReadingPage(
            CustomReadingData(data.target, data.title, source, data.chapters),
            page,
            ep,
          );
          break;
      }
      final navigator = App.navigatorKey.currentState;
      if (navigator == null) return false;
      navigator.push(_instantRoute(() => reader!));
      return true;
    } catch (e, s) {
      LogManager.addLog(
          LogLevel.error, "NavSnapshot", "restore reader failed: $e\n$s");
      return false;
    }
  }
}

/// Small widget that keeps a page's snapshot entry alive for exactly as long
/// as the page is mounted. Used by pages that do not own a `State` class.
class NavSnapshotScope extends StatefulWidget {
  const NavSnapshotScope({
    super.key,
    required this.data,
    required this.child,
  });

  final Map<String, dynamic> data;

  final Widget child;

  @override
  State<NavSnapshotScope> createState() => _NavSnapshotScopeState();
}

class _NavSnapshotScopeState extends State<NavSnapshotScope> {
  Object? _token;

  @override
  void initState() {
    _token = NavSnapshot.enter(widget.data);
    super.initState();
  }

  @override
  void dispose() {
    NavSnapshot.leave(_token);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => widget.child;
}
