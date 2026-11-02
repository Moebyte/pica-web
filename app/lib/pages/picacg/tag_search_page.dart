import 'package:flutter/material.dart';
import 'package:pica_comic/base.dart';
import 'package:pica_comic/comic_source/built_in/picacg.dart';
import 'package:pica_comic/components/components.dart';
import 'package:pica_comic/network/base_comic.dart';
import 'package:pica_comic/network/picacg_network/methods.dart';
import 'package:pica_comic/network/res.dart';
import 'package:pica_comic/tools/translations.dart';

/// Picacg multi-tag search.
///
/// The backend supports `keyword` + a `categories` array (AND). Category names
/// such as NTR/同人 are passed to the server; ordinary tags beyond the keyword
/// are filtered locally because the API only accepts one keyword.
class PicacgTagSearchPage extends StatefulWidget {
  const PicacgTagSearchPage({
    super.key,
    required this.tags,
    this.sort = "dd",
  });

  final List<String> tags;
  final String sort;

  @override
  State<PicacgTagSearchPage> createState() => _PicacgTagSearchPageState();
}

class _PicacgTagSearchPageState extends State<PicacgTagSearchPage> {
  late final _PicacgTagLoader _loader =
      _PicacgTagLoader(widget.tags, widget.sort);

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text("多标签检索".tl)),
      body: _PicacgTagComicList(
        loader: _loader,
        tags: widget.tags,
      ),
    );
  }
}

class _PicacgTagComicList extends ComicsPage<BaseComic> {
  const _PicacgTagComicList({required this.loader, required this.tags});

  final _PicacgTagLoader loader;
  final List<String> tags;

  @override
  String? get title => null;

  @override
  String get sourceKey => "picacg";

  @override
  String? get tag => "picacg multi-tag ${tags.join('|')}";

  @override
  bool get showPageIndicator => false;

  @override
  Widget? get header => SliverToBoxAdapter(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Wrap(
                spacing: 6,
                runSpacing: 2,
                children: [
                  for (final tag in tags)
                    Chip(
                      label: Text(tag),
                      visualDensity: VisualDensity.compact,
                    ),
                ],
              ),
              const SizedBox(height: 4),
              Text(
                "分类由服务端 AND 过滤，其余标签在结果内过滤".tl,
                style: const TextStyle(fontSize: 12),
              ),
            ],
          ),
        ),
      );

  @override
  Future<Res<List<BaseComic>>> getComics(int i) => loader.load(i);
}

class _PicacgTagLoader {
  _PicacgTagLoader(this.rawTags, this.sort);

  final List<String> rawTags;
  final String sort;

  late List<String> categories;
  late String keyword;
  late List<String> required;
  late List<String> excluded;

  bool _initialized = false;
  bool _exhausted = false;
  int _apiPage = 1;
  final List<BaseComic> _buffer = [];

  Future<void> _init() async {
    if (_initialized) return;
    final candidates = <String>[];
    final excludedTags = <String>[];
    for (final raw in rawTags) {
      final text = raw.trim();
      if (text.isEmpty) continue;
      if (text.startsWith("-") && text.length > 1) {
        excludedTags.add(text.substring(1).trim());
      } else {
        candidates.add(text);
      }
    }

    categories =
        candidates.where(picacgCategoryNames.contains).toSet().toList();
    final keywordTags = candidates
        .where((tag) => !picacgCategoryNames.contains(tag))
        .toSet()
        .toList();

    if (keywordTags.isEmpty) {
      keyword = "";
      required = const [];
    } else if (keywordTags.length == 1) {
      keyword = keywordTags.first;
      required = const [];
    } else {
      // Choose the narrowest keyword: it reduces the pages we have to scan
      // while the remaining tags are filtered locally.
      var bestIndex = 0;
      var bestTotal = 1 << 62;
      final probe = keywordTags.take(4).toList();
      final totals = await Future.wait(probe.map(
          (tag) => PicacgNetwork().searchTotal(tag, categories: categories)));
      for (var i = 0; i < totals.length; i++) {
        final total = totals[i];
        if (total != null && total < bestTotal) {
          bestTotal = total;
          bestIndex = i;
        }
      }
      keyword = keywordTags[bestIndex];
      required = List<String>.from(keywordTags)..removeAt(bestIndex);
      if (keywordTags.length > 4) {
        required.addAll(keywordTags.skip(4));
      }
    }
    excluded = excludedTags;
    _initialized = true;

    final label = rawTags.join(" ");
    if (label.isNotEmpty) {
      appdata.searchHistory.remove(label);
      appdata.searchHistory.add(label);
      appdata.writeHistory();
    }
  }

  Future<Res<List<BaseComic>>> load(int uiPage) async {
    await _init();
    var scanned = 0;
    final maxScan = uiPage == 1 ? 20 : 12;
    while (_buffer.length < 20 && !_exhausted && scanned < maxScan) {
      final res = await PicacgNetwork().search(
        keyword,
        sort,
        _apiPage,
        categories: categories,
        requiredTags: required,
        excludedTags: excluded,
      );
      scanned++;
      if (res.error) {
        if (_buffer.isEmpty) {
          return Res(null, errorMessage: res.errorMessage);
        }
        break;
      }
      _buffer.addAll(res.data);
      final pages = res.subData is int ? res.subData as int : null;
      if (pages != null && _apiPage >= pages) {
        _exhausted = true;
      }
      _apiPage++;
    }
    final out = _buffer.take(20).toList();
    if (out.isNotEmpty) {
      _buffer.removeRange(0, out.length);
    }
    return Res(out, subData: _exhausted ? uiPage + 1 : 999);
  }
}
