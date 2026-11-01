import 'dart:collection';

import 'package:flutter/material.dart';
import 'package:pica_comic/base.dart';
import 'package:pica_comic/comic_source/comic_source.dart';
import 'package:pica_comic/tools/translations.dart';

/// Picks several tags and returns them; the caller joins them with spaces.
///
/// Picacg's advanced search and JM's search both treat space-separated terms
/// as "all of these" (AND); a leading `-` excludes a term.
Future<List<String>?> showTagSearchSheet(
  BuildContext context, {
  required String sourceKey,
  Map<String, List<String>>? groups,
  List<String> initialSelected = const [],
}) {
  return showModalBottomSheet<List<String>>(
    context: context,
    isScrollControlled: true,
    showDragHandle: true,
    builder: (context) => _TagSearchSheet(
      sourceKey: sourceKey,
      groups: groups,
      initialSelected: initialSelected,
    ),
  );
}

class _TagSearchSheet extends StatefulWidget {
  const _TagSearchSheet({
    required this.sourceKey,
    required this.groups,
    required this.initialSelected,
  });

  final String sourceKey;
  final Map<String, List<String>>? groups;
  final List<String> initialSelected;

  @override
  State<_TagSearchSheet> createState() => _TagSearchSheetState();
}

class _TagSearchSheetState extends State<_TagSearchSheet> {
  late final LinkedHashSet<String> _selected =
      LinkedHashSet.of(widget.initialSelected);
  final TextEditingController _custom = TextEditingController();
  final FocusNode _customFocus = FocusNode();

  List<String> get _favorites {
    final name = ComicSource.find(widget.sourceKey)?.name.tlEN;
    if (name == null) return const [];
    final prefix = "$name:";
    return appdata.favoriteTags
        .where((tag) => tag.startsWith(prefix))
        .map((tag) => tag.substring(prefix.length).replaceAll('"', '').trim())
        .where((tag) => tag.isNotEmpty)
        .toList();
  }

  void _toggle(String tag) {
    setState(() {
      if (!_selected.remove(tag)) {
        _selected.add(tag);
      }
    });
  }

  void _addCustom() {
    final tag = _custom.text.trim();
    if (tag.isEmpty) return;
    setState(() {
      _selected.add(tag);
      _custom.clear();
    });
    // Keep the keyboard/focus so several tags can be typed in a row.
    Future.microtask(_customFocus.requestFocus);
  }

  Widget _chip(String tag) {
    return FilterChip(
      label: Text(tag, overflow: TextOverflow.ellipsis),
      selected: _selected.contains(tag),
      onSelected: (_) => _toggle(tag),
    );
  }

  @override
  void dispose() {
    _custom.dispose();
    _customFocus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final groups = <MapEntry<String, List<String>>>[];
    for (final entry
        in widget.groups?.entries ?? const <MapEntry<String, List<String>>>[]) {
      final tags = entry.value
          .map((tag) => tag.trim())
          .where((tag) => tag.isNotEmpty)
          .toList();
      if (tags.isNotEmpty) {
        groups.add(MapEntry(entry.key, tags));
      }
    }
    final allTags = <String>{
      for (final entry in groups) ...entry.value,
    };
    final customSelected =
        _selected.where((tag) => !allTags.contains(tag)).toList();
    final favorites = _favorites;
    final theme = Theme.of(context);

    return Padding(
      padding: EdgeInsets.only(bottom: MediaQuery.viewInsetsOf(context).bottom),
      child: ConstrainedBox(
        constraints: BoxConstraints(
          maxHeight: MediaQuery.sizeOf(context).height * 0.82,
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.filter_alt_outlined),
              title: Text("多标签检索".tl),
              subtitle: Text("选中的标签同时生效；前缀 - 表示排除".tl),
            ),
            Flexible(
              child: SingleChildScrollView(
                padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    if (customSelected.isNotEmpty) ...[
                      _sectionTitle(theme, "已选".tl),
                      Wrap(
                        spacing: 8,
                        runSpacing: 4,
                        children: [
                          for (final tag in customSelected)
                            InputChip(
                              label: Text(tag),
                              onDeleted: () => _toggle(tag),
                            ),
                        ],
                      ),
                      const SizedBox(height: 12),
                    ],
                    if (favorites.isNotEmpty) ...[
                      _sectionTitle(theme, "收藏标签".tl),
                      Wrap(
                        spacing: 8,
                        runSpacing: 4,
                        children: [for (final tag in favorites) _chip(tag)],
                      ),
                      const SizedBox(height: 12),
                    ],
                    for (final entry in groups) ...[
                      _sectionTitle(theme, entry.key),
                      Wrap(
                        spacing: 8,
                        runSpacing: 4,
                        children: [
                          for (final tag in entry.value) _chip(tag),
                        ],
                      ),
                      const SizedBox(height: 12),
                    ],
                    Row(
                      children: [
                        Expanded(
                          child: TextField(
                            controller: _custom,
                            focusNode: _customFocus,
                            decoration: InputDecoration(
                              isDense: true,
                              border: const OutlineInputBorder(),
                              hintText: "输入标签后回车添加".tl,
                            ),
                            textInputAction: TextInputAction.done,
                            onSubmitted: (_) => _addCustom(),
                          ),
                        ),
                        const SizedBox(width: 8),
                        IconButton.filledTonal(
                          onPressed: _addCustom,
                          icon: const Icon(Icons.add),
                          tooltip: "添加".tl,
                        ),
                      ],
                    ),
                  ],
                ),
              ),
            ),
            SafeArea(
              top: false,
              child: Padding(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
                child: Row(
                  children: [
                    Text("已选 ${_selected.length} 个".tl),
                    const Spacer(),
                    if (_selected.isNotEmpty)
                      TextButton(
                        onPressed: () => setState(_selected.clear),
                        child: Text("清空".tl),
                      ),
                    const SizedBox(width: 8),
                    FilledButton(
                      onPressed: _selected.isEmpty
                          ? null
                          : () => Navigator.pop(context, _selected.toList()),
                      child: Text("搜索".tl),
                    ),
                  ],
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _sectionTitle(ThemeData theme, String text) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 6),
      child: Text(
        text,
        style: theme.textTheme.titleSmall?.copyWith(
          color: theme.colorScheme.onSurfaceVariant,
        ),
      ),
    );
  }
}
