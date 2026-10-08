def total(values: list[int]) -> int:
    return sum(values)


def scaled(values: list[int], factor: int = 2) -> list[int]:
    return [v * factor for v in values]
