import unittest
import sys, pathlib
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))

import picker


class PickerStateTests(unittest.TestCase):
    def test_default_state(self):
        s = picker.PickerState(items=["a", "b", "c"])
        self.assertEqual(s.page, 0)
        self.assertEqual(s.include_remote, False)
        self.assertEqual(s.query, "")

    def test_pagination_pages_calculation(self):
        s = picker.PickerState(items=[str(i) for i in range(25)])
        self.assertEqual(picker.total_pages(s), 3)  # 10 per page

    def test_page_slice(self):
        s = picker.PickerState(items=[str(i) for i in range(25)], page=2)
        self.assertEqual(picker.current_page_items(s), ["20", "21", "22", "23", "24"])

    def test_clamp_page_when_query_shrinks_results(self):
        s = picker.PickerState(items=[str(i) for i in range(5)], page=4)
        # page is clamped lazily by current_page_items / total_pages
        self.assertEqual(picker.total_pages(s), 1)
        self.assertEqual(picker.current_page_items(s), ["0", "1", "2", "3", "4"])


class RenderTests(unittest.TestCase):
    def test_header_lists_page_and_total(self):
        s = picker.PickerState(items=["foo", "bar"], page=0)
        text, _ = picker.render(s)
        self.assertIn("page 1/1", text)
        self.assertIn("local", text)

    def test_header_says_remote_when_toggled(self):
        s = picker.PickerState(items=["foo"], include_remote=True)
        text, _ = picker.render(s)
        self.assertIn("local + remote", text)

    def test_header_shows_filter_line_when_query(self):
        s = picker.PickerState(items=["foo"], query="bar")
        text, _ = picker.render(s)
        self.assertIn("Filter: «bar»", text)

    def test_repo_rows_use_pick_callback(self):
        s = picker.PickerState(items=["foo", "bar"])
        _, kb = picker.render(s)
        # First row: "foo" with callback "pick:0"
        self.assertEqual(kb[0][0]["text"], "foo")
        self.assertEqual(kb[0][0]["callback_data"], "pick:0")
        self.assertEqual(kb[1][0]["callback_data"], "pick:1")

    def test_control_row_has_prev_next_and_toggles(self):
        s = picker.PickerState(items=[str(i) for i in range(25)], page=1)
        _, kb = picker.render(s)
        # Find prev/next row
        prev_next = next(row for row in kb if any("Prev" in b["text"] for b in row))
        labels = [b["text"] for b in prev_next]
        self.assertIn("◀ Prev", labels)
        self.assertIn("Next ▶", labels)

    def test_remote_only_rows_get_globe_prefix(self):
        # remote_set marks items that are remote-only
        s = picker.PickerState(
            items=["foo", "bar"], remote_only={"bar"}
        )
        _, kb = picker.render(s)
        self.assertEqual(kb[0][0]["text"], "foo")
        self.assertEqual(kb[1][0]["text"], "🌐 bar")


if __name__ == "__main__":
    unittest.main()
