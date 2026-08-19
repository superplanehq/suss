pub fn ready() -> bool {
    true
}

#[test]
fn is_ready() {
    assert!(ready());
}
