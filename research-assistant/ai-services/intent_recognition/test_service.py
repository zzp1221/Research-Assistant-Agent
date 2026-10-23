import os
import unittest

import main


class IntentServiceTests(unittest.TestCase):
    def test_extract_json_object_handles_code_fence(self):
        fence = chr(96) * 3
        raw = fence + "json" + chr(10) + '{"intent_type":"General","confidence":0.9,"entities":{}}' + chr(10) + fence
        self.assertEqual(
            main._extract_json_object(raw),
            '{"intent_type":"General","confidence":0.9,"entities":{}}',
        )

    def test_parse_intent_accepts_known_label(self):
        parsed = main._parse_intent(
            '{"intent_type":"Paper_Reproduction","confidence":0.87,"entities":{"paper_title":"Apriori"}}',
            "deepseek-v4.1-flash-1017",
            12,
            1,
        )
        self.assertEqual(parsed.intent_type, "Paper_Reproduction")
        self.assertEqual(parsed.entities["paper_title"], "Apriori")
        self.assertEqual(parsed.attempts, 1)

    def test_parse_intent_keeps_reasoning(self):
        parsed = main._parse_intent(
            '{"intent_type":"Code_Execution","confidence":0.91,"entities":{"needs_plot":true},"reasoning":"pure plot request"}',
            "deepseek-v4.1-flash-1017",
            8,
            1,
        )
        self.assertEqual(parsed.reasoning, "pure plot request")
        self.assertTrue(parsed.entities["needs_plot"])

    def test_parse_intent_rejects_unknown_label(self):
        with self.assertRaises(RuntimeError):
            main._parse_intent('{"intent_type":"Made_Up","confidence":0.9,"entities":{}}', "model", 1, 1)

    def test_health_reports_configured_state(self):
        old_key = os.environ.get("OPENAI_API_KEY")
        try:
            os.environ["OPENAI_API_KEY"] = "test-key"
            self.assertTrue(main.health_check()["ok"])
            os.environ.pop("OPENAI_API_KEY", None)
            self.assertFalse(main.health_check()["ok"])
        finally:
            if old_key is None:
                os.environ.pop("OPENAI_API_KEY", None)
            else:
                os.environ["OPENAI_API_KEY"] = old_key


if __name__ == "__main__":
    unittest.main()
