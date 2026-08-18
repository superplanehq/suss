pub fn name() -> &'static str {
    "tool"
}

#[test]
fn reports_name() {
    assert_eq!(name(), "tool");
}
