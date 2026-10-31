/// Web-compatible synchronous SQLite facade.
///
/// Native builds use `package:sqlite3` (dart:ffi). Web builds use
/// `package:sqlite3/wasm.dart` backed by an IndexedDB virtual file system
/// (with an in-memory fallback). `initWebDatabase()` must be awaited once
/// during startup on web before any database is opened.
///
/// The API intentionally mirrors the tiny subset of the sqlite3 API used by
/// this app: `select`, `execute` and `dispose`, with rows indexable by
/// column name or index.
library;

export 'sqlite_io.dart'
    if (dart.library.js_interop) 'sqlite_web.dart';
